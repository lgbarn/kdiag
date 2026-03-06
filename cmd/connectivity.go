package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// ConnectivityResult holds the outcome of a connectivity test.
type ConnectivityResult struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Protocol    string `json:"protocol"`
	Success     bool   `json:"success"`
	LatencyMs   int64  `json:"latency_ms"`
	StatusCode  int    `json:"status_code,omitempty"`
	Error       string `json:"error,omitempty"`
}

var (
	connectivityPort     int
	connectivityProtocol string
)

var connectivityCmd = &cobra.Command{
	Use:   "connectivity <source-pod> <destination>",
	Short: "Test network connectivity from a source pod to a destination",
	Long:  "Test TCP or HTTP connectivity from a source pod to a destination pod, service, or host:port.",
	Args:  cobra.ExactArgs(2),
	RunE:  runConnectivity,
}

func init() {
	connectivityCmd.Flags().IntVarP(&connectivityPort, "port", "p", 0, "Destination port for pod/service targets")
	connectivityCmd.Flags().StringVar(&connectivityProtocol, "protocol", "tcp", "Protocol to test: tcp or http")
	rootCmd.AddCommand(connectivityCmd)
}

func runConnectivity(cmd *cobra.Command, args []string) error {
	srcPod := args[0]
	dst := args[1]

	if connectivityProtocol != "tcp" && connectivityProtocol != "http" {
		return fmt.Errorf("unsupported protocol %q: must be tcp or http", connectivityProtocol)
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

	// Resolve source pod.
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] resolving source pod %q in namespace %q\n", srcPod, namespace)
	}

	srcPodObj, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, srcPod, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("error: source pod %q not found in namespace %q", srcPod, namespace)
		}
		return fmt.Errorf("error getting source pod %q: %w", srcPod, err)
	}
	if srcPodObj.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("error: source pod %q is not Running (phase: %s)", srcPod, srcPodObj.Status.Phase)
	}

	// Resolve destination into host + port + protocol.
	var (
		dstHost     string
		dstPort     string
		protocol    string
	)

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] resolving destination %q\n", dst)
	}

	if strings.Contains(dst, ":") {
		// host:port provided directly.
		parts := strings.SplitN(dst, ":", 2)
		dstHost = parts[0]
		dstPort = parts[1]
		portNum, portErr := strconv.Atoi(dstPort)
		if portErr != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("invalid port %q: must be a number between 1 and 65535", dstPort)
		}
		protocol = connectivityProtocol
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] destination is host:port — host=%q port=%q\n", dstHost, dstPort)
		}
	} else {
		// Try as Service.
		svc, svcErr := client.Clientset.CoreV1().Services(namespace).Get(ctx, dst, metav1.GetOptions{})
		if svcErr == nil {
			dstHost = svc.Spec.ClusterIP
			if connectivityPort != 0 {
				dstPort = strconv.Itoa(connectivityPort)
			} else if len(svc.Spec.Ports) > 0 {
				dstPort = strconv.Itoa(int(svc.Spec.Ports[0].Port))
			} else {
				return fmt.Errorf("error: service %q has no ports; use --port to specify", dst)
			}

			// Auto-detect protocol from service port info.
			protocol = connectivityProtocol
			if len(svc.Spec.Ports) > 0 {
				portNum := int(svc.Spec.Ports[0].Port)
				portName := strings.ToLower(svc.Spec.Ports[0].Name)
				if strings.Contains(portName, "http") || portNum == 80 || portNum == 443 {
					protocol = "http"
				}
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] resolved %q as service — host=%q port=%q protocol=%q\n", dst, dstHost, dstPort, protocol)
			}
		} else if apierrors.IsNotFound(svcErr) {
			// Try as Pod.
			pod, podErr := client.Clientset.CoreV1().Pods(namespace).Get(ctx, dst, metav1.GetOptions{})
			if podErr != nil {
				if apierrors.IsNotFound(podErr) {
					return fmt.Errorf("error: destination %q not found as a service or pod in namespace %q", dst, namespace)
				}
				return fmt.Errorf("error getting destination pod %q: %w", dst, podErr)
			}
			dstHost = pod.Status.PodIP
			if connectivityPort == 0 {
				return fmt.Errorf("error: --port is required when destination is a pod")
			}
			dstPort = strconv.Itoa(connectivityPort)
			protocol = connectivityProtocol
			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] resolved %q as pod — host=%q port=%q protocol=%q\n", dst, dstHost, dstPort, protocol)
			}
		} else {
			return fmt.Errorf("error looking up service %q: %w", dst, svcErr)
		}
	}

	// Build connectivity command.
	var connectCmd []string
	switch protocol {
	case "http":
		scheme := "http"
		if dstPort == "443" {
			scheme = "https"
		}
		url := scheme + "://" + dstHost + ":" + dstPort + "/"
		connectCmd = []string{
			"curl", "-sS", "--connect-timeout", "5",
			"-o", "/dev/null",
			"-w", "%{http_code} %{time_total}",
			url,
		}
	default:
		// tcp
		connectCmd = []string{"nc", "-zv", "-w", "5", dstHost, dstPort}
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] creating ephemeral container in pod %s/%s\n", namespace, srcPod)
		fmt.Fprintf(os.Stderr, "[kdiag] running connectivity command: %v\n", connectCmd)
	}

	// Run connectivity command via ephemeral container (RBAC pre-flight + create + wait + exec).
	var stdout, stderr bytes.Buffer
	start := time.Now()
	execErr := k8s.RunInEphemeralContainer(ctx, client, k8s.EphemeralExecOpts{
		PodName:         srcPod,
		Namespace:       namespace,
		Image:           GetDebugImage(),
		ImagePullSecret: GetImagePullSecret(),
		Command:         connectCmd,
		Stdout:          &stdout,
		Stderr:          &stderr,
	})
	elapsed := time.Since(start)

	// Parse result.
	result := ConnectivityResult{
		Source:      srcPod,
		Destination: dstHost + ":" + dstPort,
		Protocol:    protocol,
	}

	switch protocol {
	case "http":
		var parseErr error
		result, parseErr = parseHTTPResult(result, stdout.String(), elapsed)
		if parseErr != nil {
			return fmt.Errorf("error parsing HTTP result: %w", parseErr)
		}
	default:
		if execErr == nil {
			result.Success = true
			result.LatencyMs = elapsed.Milliseconds()
		} else {
			result.Success = false
			result.Error = strings.TrimSpace(stderr.String())
			if result.Error == "" {
				result.Error = execErr.Error()
			}
			result.LatencyMs = elapsed.Milliseconds()
		}
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
	printer.PrintHeader("SOURCE", "DESTINATION", "PROTOCOL", "STATUS", "LATENCY", "DETAILS")

	statusStr := "OK"
	if !result.Success {
		statusStr = "FAILED"
	}
	latencyStr := fmt.Sprintf("%dms", result.LatencyMs)
	details := ""
	if result.StatusCode != 0 {
		details = fmt.Sprintf("HTTP %d", result.StatusCode)
	}
	if result.Error != "" {
		if details != "" {
			details += " — " + result.Error
		} else {
			details = result.Error
		}
	}

	printer.PrintRow(result.Source, result.Destination, result.Protocol, statusStr, latencyStr, details)
	return printer.Flush()
}

// parseHTTPResult parses curl's "-w %{http_code} %{time_total}" stdout into the result.
func parseHTTPResult(result ConnectivityResult, raw string, elapsed time.Duration) (ConnectivityResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		result.Success = false
		result.LatencyMs = elapsed.Milliseconds()
		result.Error = "no output from curl"
		return result, nil
	}

	parts := strings.Fields(raw)
	if len(parts) >= 1 {
		code, err := strconv.Atoi(parts[0])
		if err == nil {
			result.StatusCode = code
			// 2xx or 3xx is success.
			result.Success = code >= 200 && code < 400
		}
	}

	if len(parts) >= 2 {
		// time_total is in seconds (float), convert to ms.
		secs, err := strconv.ParseFloat(parts[1], 64)
		if err == nil {
			result.LatencyMs = int64(secs * 1000)
		}
	} else {
		result.LatencyMs = elapsed.Milliseconds()
	}

	return result, nil
}
