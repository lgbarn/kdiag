package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lgbarn/kdiag/pkg/dns"
	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

var dnsCmd = &cobra.Command{
	Use:   "dns <pod-or-service>",
	Short: "Test DNS resolution from a pod's perspective and check CoreDNS health",
	Args:  cobra.ExactArgs(1),
	RunE:  runDNS,
}

func init() {
	rootCmd.AddCommand(dnsCmd)
}

func runDNS(cmd *cobra.Command, args []string) error {
	target := StripPodPrefix(args[0])

	if err := ValidateDebugImage(); err != nil {
		return err
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] building kubernetes client\n")
	}

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	// Resolve target type — try Service first, then Pod.
	var (
		fqdn    string
		podName string
	)

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] resolving target %q in namespace %q\n", target, namespace)
	}

	svc, svcErr := client.Clientset.CoreV1().Services(namespace).Get(ctx, target, metav1.GetOptions{})
	if svcErr == nil {
		// Target is a service — build FQDN from service name and find a backing pod.
		fqdn = dns.BuildFQDN(svc.Name, namespace)
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] resolved %q as service with FQDN %q\n", target, fqdn)
		}

		// Find a Running pod backing the service via label selector.
		selector := formatLabelSelector(svc.Spec.Selector)
		if selector == "" {
			return fmt.Errorf("error: service %q in namespace %q has no pod selector", target, namespace)
		}

		podList, listErr := client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if listErr != nil {
			return fmt.Errorf("error listing pods for service %q: %w", target, listErr)
		}

		podName = findRunningPod(podList.Items)
		if podName == "" {
			return fmt.Errorf("error: no Running pods found backing service %q in namespace %q", target, namespace)
		}
	} else if apierrors.IsNotFound(svcErr) {
		// Not a service — try as a pod.
		pod, podErr := client.Clientset.CoreV1().Pods(namespace).Get(ctx, target, metav1.GetOptions{})
		if podErr != nil {
			if apierrors.IsNotFound(podErr) {
				return fmt.Errorf("error: %q not found as a service or pod in namespace %q", target, namespace)
			}
			return fmt.Errorf("error getting pod %q: %w", target, podErr)
		}
		if pod.Status.Phase != corev1.PodRunning {
			return fmt.Errorf("error: pod %q is not Running (phase: %s)", target, pod.Status.Phase)
		}
		fqdn = dns.BuildFQDN(pod.Name, namespace)
		podName = pod.Name
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] resolved %q as pod with FQDN %q\n", target, fqdn)
		}
	} else {
		return fmt.Errorf("error looking up service %q: %w", target, svcErr)
	}

	// CoreDNS health check.
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] checking CoreDNS pod health\n")
	}

	coreDNSPodList, err := client.Clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=kube-dns",
	})
	if err != nil {
		return fmt.Errorf("error listing CoreDNS pods: %w", err)
	}
	coreDNSPods := dns.EvaluateCoreDNSPods(coreDNSPodList.Items)

	// Get CoreDNS service IP.
	var coreDNSIP string
	coreDNSSvc, err := client.Clientset.CoreV1().Services("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
	if err != nil {
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] warning: could not get kube-dns service IP: %v\n", err)
		}
	} else {
		coreDNSIP = coreDNSSvc.Spec.ClusterIP
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] CoreDNS IP: %s\n", coreDNSIP)
		}
	}

	// Exec dig command via ephemeral container (RBAC pre-flight + create + wait + exec).
	digCmd := dns.BuildDigCommand(fqdn, coreDNSIP)

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] creating ephemeral container in pod %s/%s\n", namespace, podName)
		fmt.Fprintf(os.Stderr, "[kdiag] running dig command: %v\n", digCmd)
	}

	var stdout, stderr bytes.Buffer
	execErr := k8s.RunInEphemeralContainer(ctx, client, k8s.EphemeralExecOpts{
		PodName:         podName,
		Namespace:       namespace,
		Image:           GetDebugImage(),
		ImagePullSecret: GetImagePullSecret(),
		Command:         digCmd,
		Stdout:          &stdout,
		Stderr:          &stderr,
	})

	// Parse dig output (even if exec returned an error, we may have partial output).
	resolved, queryTimeMs, parseErr := dns.ParseDigOutput(stdout.String())

	result := dns.DNSResult{
		Target:      target,
		FQDN:        fqdn,
		Resolved:    resolved,
		QueryTimeMs: queryTimeMs,
		CoreDNS:     coreDNSPods,
	}
	if IsVerbose() {
		result.RawOutput = stdout.String()
	}

	if execErr != nil {
		result.Error = execErr.Error()
	} else if parseErr != nil {
		result.Error = parseErr.Error()
	}

	// Output.
	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("error creating printer: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	// Table output.
	fmt.Fprintln(os.Stdout, "\nDNS Resolution")
	printer.PrintHeader("TARGET", "FQDN", "RESOLVED IPs", "QUERY TIME")
	ips := strings.Join(resolved, ", ")
	if ips == "" {
		ips = "<none>"
	}
	queryTime := fmt.Sprintf("%dms", queryTimeMs)
	printer.PrintRow(target, fqdn, ips, queryTime)
	if err := printer.Flush(); err != nil {
		return fmt.Errorf("error flushing output: %w", err)
	}

	fmt.Fprintln(os.Stdout, "\nCoreDNS Health")
	printer.PrintHeader("POD", "STATUS", "READY")
	for _, p := range coreDNSPods {
		printer.PrintRow(p.Name, p.Status, boolStr(p.Ready))
	}
	if err := printer.Flush(); err != nil {
		return fmt.Errorf("error flushing output: %w", err)
	}

	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "\nwarning: DNS query error: %s\n", result.Error)
	}

	return nil
}

// formatLabelSelector converts a map of label key=value pairs into a
// comma-separated selector string suitable for Kubernetes ListOptions.
func formatLabelSelector(selector map[string]string) string {
	parts := make([]string, 0, len(selector))
	for k, v := range selector {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// findRunningPod returns the name of the first Running pod in the list,
// or an empty string if none are Running.
func findRunningPod(pods []corev1.Pod) string {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name
		}
	}
	return ""
}
