package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// IngressResult holds the structured output of an ingress inspection.
type IngressResult struct {
	Name       string              `json:"name"`
	Namespace  string              `json:"namespace"`
	Class      string              `json:"class"`
	Controller string              `json:"controller"`
	Rules      []IngressRuleResult `json:"rules"`
	TLS        []IngressTLSResult  `json:"tls"`
	CtrlHealth string              `json:"controller_health,omitempty"`
}

// IngressRuleResult holds the check result for a single ingress rule path.
type IngressRuleResult struct {
	Host           string `json:"host"`
	Path           string `json:"path"`
	ServiceName    string `json:"service_name"`
	ServicePort    string `json:"service_port"`
	ServiceExists  bool   `json:"service_exists"`
	EndpointsReady int    `json:"endpoints_ready"`
}

// IngressTLSResult holds the check result for a TLS secret reference.
type IngressTLSResult struct {
	SecretName string   `json:"secret_name"`
	Hosts      []string `json:"hosts"`
	Exists     bool     `json:"exists"`
}

// detectIngressController returns the controller type from the Ingress spec.
func detectIngressController(ingress *networkingv1.Ingress) string {
	if ingress.Spec.IngressClassName != nil {
		return *ingress.Spec.IngressClassName
	}
	if v, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		return v
	}
	return ""
}

// countReadyEndpoints counts the number of ready addresses across all subsets.
func countReadyEndpoints(ep *corev1.Endpoints) int {
	count := 0
	for _, subset := range ep.Subsets {
		count += len(subset.Addresses)
	}
	return count
}

var ingressCmd = &cobra.Command{
	Use:   "ingress <name>",
	Short: "Inspect Ingress rules, backends, TLS secrets, and controller health",
	Args:  cobra.ExactArgs(1),
	RunE:  runIngress,
}

func init() {
	rootCmd.AddCommand(ingressCmd)
}

func runIngress(cmd *cobra.Command, args []string) error {
	name := args[0]

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting ingress %q in namespace %q\n", name, namespace)
	}

	ingress, err := client.Clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("ingress %q not found in namespace %q", name, namespace)
		}
		return fmt.Errorf("failed to get ingress %q: %w", name, err)
	}

	controller := detectIngressController(ingress)

	// Determine class label for display.
	class := ""
	if ingress.Spec.IngressClassName != nil {
		class = *ingress.Spec.IngressClassName
	} else if v, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		class = v
	}

	// Build rules results.
	var rules []IngressRuleResult
	for _, rule := range ingress.Spec.Rules {
		host := rule.Host
		if host == "" {
			host = "*"
		}
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			rr := IngressRuleResult{
				Host: host,
				Path: path.Path,
			}
			if path.Backend.Service != nil {
				svcName := path.Backend.Service.Name
				rr.ServiceName = svcName

				// Represent port as number or name.
				port := path.Backend.Service.Port
				if port.Name != "" {
					rr.ServicePort = port.Name
				} else {
					rr.ServicePort = fmt.Sprintf("%d", port.Number)
				}

				// Check if the Service exists.
				_, svcErr := client.Clientset.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{})
				if svcErr == nil {
					rr.ServiceExists = true
				} else if !apierrors.IsNotFound(svcErr) {
					if IsVerbose() {
						fmt.Fprintf(os.Stderr, "[kdiag] warning: error checking service %q: %v\n", svcName, svcErr)
					}
				}

				// Count ready endpoints.
				ep, epErr := client.Clientset.CoreV1().Endpoints(namespace).Get(ctx, svcName, metav1.GetOptions{})
				if epErr == nil {
					rr.EndpointsReady = countReadyEndpoints(ep)
				}
			}
			rules = append(rules, rr)
		}
	}

	// Build TLS results.
	var tlsResults []IngressTLSResult
	for _, tls := range ingress.Spec.TLS {
		tr := IngressTLSResult{
			SecretName: tls.SecretName,
			Hosts:      tls.Hosts,
		}
		_, secretErr := client.Clientset.CoreV1().Secrets(namespace).Get(ctx, tls.SecretName, metav1.GetOptions{})
		if secretErr == nil {
			tr.Exists = true
		} else if !apierrors.IsNotFound(secretErr) {
			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] warning: error checking secret %q: %v\n", tls.SecretName, secretErr)
			}
		}
		tlsResults = append(tlsResults, tr)
	}

	ctrlHealth := checkControllerHealth(ctx, client, controller)

	result := IngressResult{
		Name:       name,
		Namespace:  namespace,
		Class:      class,
		Controller: controller,
		Rules:      rules,
		TLS:        tlsResults,
		CtrlHealth: ctrlHealth,
	}

	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	return printIngressTable(result)
}

// checkControllerHealth returns a readiness summary string for the ingress controller pods.
func checkControllerHealth(ctx context.Context, client *k8s.Client, controller string) string {
	var labelSelector, namespace string

	switch strings.ToLower(controller) {
	case "alb":
		labelSelector = "app.kubernetes.io/name=aws-load-balancer-controller"
		namespace = "kube-system"
	case "nginx":
		labelSelector = "app.kubernetes.io/name=ingress-nginx"
		namespace = "ingress-nginx"
	default:
		return ""
	}

	pods, err := client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil || len(pods.Items) == 0 {
		if controller == "nginx" {
			// Fallback to kube-system for nginx.
			pods, err = client.Clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil || len(pods.Items) == 0 {
				return "no controller pods found"
			}
		} else {
			return "no controller pods found"
		}
	}

	total := len(pods.Items)
	ready := 0
	for _, pod := range pods.Items {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}

	return fmt.Sprintf("%d/%d pods ready", ready, total)
}

// findIngressesForPod finds Ingresses that route to Services selecting this pod.
// It returns the matched ingress rules, TLS results, and any API error encountered.
func findIngressesForPod(ctx context.Context, client *k8s.Client, namespace string, pod *corev1.Pod) ([]IngressRuleResult, []IngressTLSResult, error) {
	// Find services that select this pod.
	svcList, err := client.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list services: %w", err)
	}

	podSvcNames := map[string]bool{}
	for _, svc := range svcList.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		match := true
		for k, v := range svc.Spec.Selector {
			if pod.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			podSvcNames[svc.Name] = true
		}
	}

	if len(podSvcNames) == 0 {
		return nil, nil, nil
	}

	// Find ingresses referencing those services.
	ingList, err := client.Clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list ingresses: %w", err)
	}

	var rules []IngressRuleResult
	var tlsResults []IngressTLSResult
	seenTLS := map[string]bool{}

	for _, ing := range ingList.Items {
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service == nil {
					continue
				}
				if !podSvcNames[path.Backend.Service.Name] {
					continue
				}
				rr := IngressRuleResult{
					Host:          rule.Host,
					Path:          path.Path,
					ServiceName:   path.Backend.Service.Name,
					ServiceExists: true,
				}
				if path.Backend.Service.Port.Name != "" {
					rr.ServicePort = path.Backend.Service.Port.Name
				} else {
					rr.ServicePort = fmt.Sprintf("%d", path.Backend.Service.Port.Number)
				}
				ep, epErr := client.Clientset.CoreV1().Endpoints(namespace).Get(ctx, rr.ServiceName, metav1.GetOptions{})
				if epErr == nil {
					rr.EndpointsReady = countReadyEndpoints(ep)
				}
				rules = append(rules, rr)
			}
		}

		// Check TLS for matching ingresses only if this ingress references our services.
		hasMatch := false
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil && podSvcNames[path.Backend.Service.Name] {
					hasMatch = true
					break
				}
			}
			if hasMatch {
				break
			}
		}
		if hasMatch {
			for _, tls := range ing.Spec.TLS {
				if seenTLS[tls.SecretName] {
					continue
				}
				seenTLS[tls.SecretName] = true
				tr := IngressTLSResult{
					SecretName: tls.SecretName,
					Hosts:      tls.Hosts,
				}
				if tls.SecretName != "" {
					_, secErr := client.Clientset.CoreV1().Secrets(namespace).Get(ctx, tls.SecretName, metav1.GetOptions{})
					tr.Exists = secErr == nil
				}
				tlsResults = append(tlsResults, tr)
			}
		}
	}

	return rules, tlsResults, nil
}

// printIngressTable writes a human-readable table view of an IngressResult.
func printIngressTable(result IngressResult) error {
	fmt.Fprintf(os.Stdout, "\nIngress: %s  Namespace: %s  Class: %s\n",
		result.Name, result.Namespace, result.Class)

	// Rules table.
	fmt.Fprintln(os.Stdout, "\nRules:")
	tp := output.NewTablePrinter(os.Stdout)
	tp.PrintHeader("HOST", "PATH", "SERVICE", "PORT", "SVC EXISTS", "ENDPOINTS")
	for _, r := range result.Rules {
		tp.PrintRow(
			r.Host,
			r.Path,
			r.ServiceName,
			r.ServicePort,
			boolStr(r.ServiceExists),
			fmt.Sprintf("%d", r.EndpointsReady),
		)
	}
	if err := tp.Flush(); err != nil {
		return fmt.Errorf("error flushing rules table: %w", err)
	}

	// TLS table.
	if len(result.TLS) > 0 {
		fmt.Fprintln(os.Stdout, "\nTLS:")
		tp2 := output.NewTablePrinter(os.Stdout)
		tp2.PrintHeader("SECRET", "HOSTS", "STATUS")
		for _, t := range result.TLS {
			status := "missing"
			if t.Exists {
				status = "found"
			}
			tp2.PrintRow(t.SecretName, strings.Join(t.Hosts, ", "), status)
		}
		if err := tp2.Flush(); err != nil {
			return fmt.Errorf("error flushing TLS table: %w", err)
		}
	}

	// Controller health.
	if result.CtrlHealth != "" {
		fmt.Fprintf(os.Stdout, "\nController Health: %s\n", result.CtrlHealth)
	}

	return nil
}
