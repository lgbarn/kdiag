package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// TraceResult holds the full network path from source pod through service to endpoint pods.
type TraceResult struct {
	Source    TraceHop   `json:"source"`
	Service   TraceHop   `json:"service"`
	Endpoints []TraceHop `json:"endpoints"`
}

// TraceHop represents a single network hop in the trace path.
type TraceHop struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	IP        string `json:"ip"`
	NodeName  string `json:"node_name,omitempty"`
	NodeAZ    string `json:"node_az,omitempty"`
}

var traceCmd = &cobra.Command{
	Use:   "trace <source-pod> <destination-service>",
	Short: "Map the network path from a pod to a service",
	Long:  "Trace the Kubernetes network path: source pod IP -> service ClusterIP -> endpoint pod IPs -> nodes.",
	Args:  cobra.ExactArgs(2),
	RunE:  runTrace,
}

func init() {
	rootCmd.AddCommand(traceCmd)
}

func runTrace(cmd *cobra.Command, args []string) error {
	srcPod := StripPodPrefix(args[0])
	dstService := args[1]

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] resolving source pod %q in namespace %q\n", srcPod, namespace)
	}

	// Resolve source pod.
	pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, srcPod, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("pod %q not found in namespace %q", srcPod, namespace)
		}
		return fmt.Errorf("failed to get pod %q: %w", srcPod, err)
	}

	srcIP := pod.Status.PodIP
	srcNode := pod.Spec.NodeName

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] source pod IP: %s, node: %s\n", srcIP, srcNode)
		fmt.Fprintf(os.Stderr, "[kdiag] resolving destination service %q\n", dstService)
	}

	// Resolve destination service.
	svc, err := client.Clientset.CoreV1().Services(namespace).Get(ctx, dstService, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("service %q not found in namespace %q", dstService, namespace)
		}
		return fmt.Errorf("failed to get service %q: %w", dstService, err)
	}

	clusterIP := svc.Spec.ClusterIP

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] service ClusterIP: %s\n", clusterIP)
		fmt.Fprintf(os.Stderr, "[kdiag] listing EndpointSlices for service %q\n", dstService)
	}

	// List EndpointSlices for the service.
	esList, err := client.Clientset.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + dstService,
	})
	if err != nil {
		return fmt.Errorf("failed to list EndpointSlices for service %q: %w", dstService, err)
	}

	// Collect endpoints and unique node names.
	endpoints := make([]TraceHop, 0)
	nodeNames := map[string]struct{}{}
	if srcNode != "" {
		nodeNames[srcNode] = struct{}{}
	}

	for _, es := range esList.Items {
		for _, ep := range es.Endpoints {
			// Skip not-ready endpoints.
			if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
				continue
			}
			if len(ep.Addresses) == 0 {
				continue
			}

			ip := ep.Addresses[0]

			nodeName := ""
			if ep.NodeName != nil {
				nodeName = *ep.NodeName
				nodeNames[nodeName] = struct{}{}
			}

			podName := ip
			epNamespace := namespace
			if ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
				podName = ep.TargetRef.Name
				if ep.TargetRef.Namespace != "" {
					epNamespace = ep.TargetRef.Namespace
				}
			}

			endpoints = append(endpoints, TraceHop{
				Name:      podName,
				Namespace: epNamespace,
				IP:        ip,
				NodeName:  nodeName,
			})
		}
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] found %d ready endpoints; resolving node AZs\n", len(endpoints))
	}

	// Resolve node AZs (best-effort).
	nodeAZs := map[string]string{}
	for nodeName := range nodeNames {
		if nodeName == "" {
			continue
		}
		node, err := client.Clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] warning: could not get node %q: %v\n", nodeName, err)
			}
			nodeAZs[nodeName] = ""
			continue
		}
		az := node.Labels["topology.kubernetes.io/zone"]
		if az == "" {
			az = node.Labels["failure-domain.beta.kubernetes.io/zone"]
		}
		nodeAZs[nodeName] = az
	}

	// Build TraceResult.
	result := TraceResult{
		Source: TraceHop{
			Name:      srcPod,
			Namespace: namespace,
			IP:        srcIP,
			NodeName:  srcNode,
			NodeAZ:    nodeAZs[srcNode],
		},
		Service: TraceHop{
			Name:      dstService,
			Namespace: namespace,
			IP:        clusterIP,
		},
	}

	for i := range endpoints {
		endpoints[i].NodeAZ = nodeAZs[endpoints[i].NodeName]
		result.Endpoints = append(result.Endpoints, endpoints[i])
	}

	// Output.
	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	// Table output.
	fmt.Fprintf(os.Stdout, "Network Path: %s -> %s\n\n", srcPod, dstService)
	printer.PrintHeader("HOP", "NAME", "NAMESPACE", "IP", "NODE", "AZ")
	printer.PrintRow("Source", result.Source.Name, result.Source.Namespace, result.Source.IP, result.Source.NodeName, result.Source.NodeAZ)
	printer.PrintRow("Service", result.Service.Name, result.Service.Namespace, result.Service.IP, result.Service.NodeName, result.Service.NodeAZ)
	for _, ep := range result.Endpoints {
		printer.PrintRow("Endpoint", ep.Name, ep.Namespace, ep.IP, ep.NodeName, ep.NodeAZ)
	}

	return printer.Flush()
}
