package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
)

// podColors is a cycling palette of terminal colors for pod name prefixes.
var podColors = []*color.Color{
	color.New(color.FgCyan),
	color.New(color.FgGreen),
	color.New(color.FgYellow),
	color.New(color.FgMagenta),
	color.New(color.FgBlue),
	color.New(color.FgHiCyan),
	color.New(color.FgHiGreen),
	color.New(color.FgHiYellow),
	color.New(color.FgHiMagenta),
	color.New(color.FgHiBlue),
}

// logLine is the JSON-L record emitted per log line in --output json mode.
type logLine struct {
	Pod       string `json:"pod"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

// prefixWriter is an io.Writer that prepends a colored pod-name prefix to
// every log line, applies optional string filtering, and serialises concurrent
// writes with a mutex. In JSON mode it emits JSON-L records instead.
type prefixWriter struct {
	mu       sync.Mutex
	base     io.Writer
	prefix   string
	filter   string
	jsonMode bool
	podName  string
}

// Write implements io.Writer. It splits p on newlines and handles each
// non-empty line individually so that the prefix appears once per line.
func (pw *prefixWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if pw.filter != "" && !strings.Contains(line, pw.filter) {
			continue
		}

		var writeErr error
		if pw.jsonMode {
			// The Kubernetes log stream with Timestamps:true prepends an RFC3339
			// timestamp followed by a single space. Split on the first space.
			ts := ""
			msg := line
			if idx := strings.Index(line, " "); idx != -1 {
				ts = line[:idx]
				msg = line[idx+1:]
			}
			rec := logLine{
				Pod:       pw.podName,
				Timestamp: ts,
				Message:   msg,
			}
			b, err := json.Marshal(rec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to marshal log line for pod %s: %v\n", pw.podName, err)
				continue
			}
			pw.mu.Lock()
			_, writeErr = fmt.Fprintf(pw.base, "%s\n", b)
			pw.mu.Unlock()
		} else {
			pw.mu.Lock()
			_, writeErr = fmt.Fprintf(pw.base, "%s%s\n", pw.prefix, line)
			pw.mu.Unlock()
		}

		if writeErr != nil {
			return 0, writeErr
		}
	}
	return len(p), nil
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail logs from pods matching a label selector with color-coded output",
	Args:  cobra.NoArgs,
	RunE:  runLogs,
}

func init() {
	logsCmd.Flags().StringP("selector", "l", "", "Label selector for pod matching (e.g. app=myapp) (required)")
	logsCmd.Flags().String("filter", "", "Only show log lines containing this string")
	logsCmd.Flags().Int("max-pods", 10, "Maximum number of concurrent pod log streams")
	logsCmd.Flags().StringP("container", "c", "", "Specific container name to tail (omit for default container)")

	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	selector, _ := cmd.Flags().GetString("selector")
	if selector == "" {
		return fmt.Errorf("--selector / -l flag is required")
	}

	filterStr, _ := cmd.Flags().GetString("filter")
	maxPods, _ := cmd.Flags().GetInt("max-pods")
	containerName, _ := cmd.Flags().GetString("container")

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing pods with selector %q in namespace %q\n", selector, namespace)
	}

	listCtx, listCancel := context.WithTimeout(context.Background(), GetTimeout())
	defer listCancel()

	pods, err := k8s.ListPodsBySelector(listCtx, client, namespace, selector)
	if err != nil {
		return fmt.Errorf("error listing pods: %w", err)
	}

	if len(pods) == 0 {
		return fmt.Errorf("no pods found matching selector %q in namespace %q", selector, namespace)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] found %d pods matching selector\n", len(pods))
	}

	if len(pods) > maxPods {
		fmt.Fprintf(os.Stderr, "warning: %d pods match selector but --max-pods is %d; tailing first %d pods\n",
			len(pods), maxPods, maxPods)
		pods = pods[:maxPods]
	}

	jsonMode := GetOutputFormat() == "json"

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	var wg sync.WaitGroup
	for i, pod := range pods {
		wg.Add(1)

		c := podColors[i%len(podColors)]
		prefix := c.Sprintf("[%s] ", pod.Name)
		pw := &prefixWriter{
			base:     color.Output,
			prefix:   prefix,
			filter:   filterStr,
			jsonMode: jsonMode,
			podName:  pod.Name,
		}

		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] starting log stream for pod %s\n", pod.Name)
		}

		go func(pod corev1.Pod, pw *prefixWriter) {
			defer wg.Done()
			if err := k8s.StreamPodLogs(ctx, client, namespace, pod.Name, containerName, pw); err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "warning: log stream ended for pod %s: %v\n", pod.Name, err)
				}
			}
		}(pod, pw)
	}

	wg.Wait()
	signal.Stop(sigCh)
	return nil
}
