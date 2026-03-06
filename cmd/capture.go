package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/lgbarn/kdiag/pkg/k8s"
)

var (
	captureFilter    string
	captureOutput    string
	captureInterface string
	captureCount     int
	captureDuration  time.Duration
)

var captureCmd = &cobra.Command{
	Use:   "capture <pod-name>",
	Short: "Capture network traffic from a pod using tcpdump",
	RunE:  runCapture,
}

func init() {
	rootCmd.AddCommand(captureCmd)

	captureCmd.Flags().StringVarP(&captureFilter, "filter", "f", "", "BPF filter expression for tcpdump")
	captureCmd.Flags().StringVarP(&captureOutput, "write", "w", "", "Write raw pcap data to file path")
	captureCmd.Flags().StringVarP(&captureInterface, "interface", "i", "any", "Network interface to capture on")
	captureCmd.Flags().IntVarP(&captureCount, "count", "c", 0, "Stop after receiving count packets (0 = unlimited)")
	captureCmd.Flags().DurationVarP(&captureDuration, "duration", "d", 0, "Stop capture after duration (e.g. 30s, 2m; 0 = unlimited)")
}

func runCapture(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("error: requires exactly 1 argument (pod name)\n\nUsage:\n  %s", cmd.UseLine())
	}
	podName := args[0]

	// Validate output path directory exists before doing any Kubernetes work.
	if captureOutput != "" {
		dir := filepath.Dir(captureOutput)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return fmt.Errorf("output directory %q does not exist", dir)
		}
		// Warn if file already exists.
		if _, err := os.Stat(captureOutput); err == nil {
			fmt.Fprintf(os.Stderr, "warning: output file %q already exists and will be overwritten\n", captureOutput)
		}
	}

	// Build Kubernetes client.
	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	// Validate image before any cluster work.
	if err := ValidateDebugImage(); err != nil {
		return err
	}

	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), GetTimeout())
	defer preflightCancel()

	// Verify pod exists.
	_, err = client.Clientset.CoreV1().Pods(namespace).Get(preflightCtx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("pod %q not found in namespace %q", podName, namespace)
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// RBAC pre-flight check.
	checks, err := k8s.CheckEphemeralContainerRBAC(preflightCtx, client.Clientset, namespace)
	if err != nil {
		return fmt.Errorf("RBAC check failed: %w", err)
	}
	if msg := k8s.FormatRBACError(checks); msg != "" {
		return fmt.Errorf("insufficient permissions:\n%s", msg)
	}

	// Validate network interface name (IFNAMSIZ = 16 on Linux, alphanumeric + dash/dot/underscore).
	if len(captureInterface) > 15 {
		return fmt.Errorf("--interface value %q is too long (max 15 chars)", captureInterface)
	}
	ifaceRe := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	if !ifaceRe.MatchString(captureInterface) {
		return fmt.Errorf("--interface value %q contains invalid characters", captureInterface)
	}

	// Build tcpdump command.
	tcpdumpCmd := []string{"tcpdump", "-i", captureInterface}

	if captureOutput != "" {
		// Raw pcap to stdout, packet-buffered.
		tcpdumpCmd = append(tcpdumpCmd, "-w", "-", "-U")
	} else {
		// Line-buffered human-readable output.
		tcpdumpCmd = append(tcpdumpCmd, "-l")
	}

	if captureCount > 0 {
		tcpdumpCmd = append(tcpdumpCmd, "-c", strconv.Itoa(captureCount))
	}

	if captureFilter != "" {
		// Defensive validation: reject null bytes and excessively long filters.
		// The real protection against shell injection is argv execution (no shell),
		// but we reject obviously malformed inputs early.
		if len(captureFilter) > 1024 {
			return fmt.Errorf("--filter value is too long (%d chars); maximum allowed is 1024", len(captureFilter))
		}
		for _, b := range []byte(captureFilter) {
			if b == 0 {
				return fmt.Errorf("--filter value contains a null byte, which is not allowed")
			}
		}
		// BPF filter goes as trailing args.
		tcpdumpCmd = append(tcpdumpCmd, captureFilter)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[verbose] pod: %s/%s\n", namespace, podName)
		fmt.Fprintf(os.Stderr, "[verbose] tcpdump command: %v\n", tcpdumpCmd)
	}

	// Set up context with optional duration timeout and SIGINT handler.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if captureDuration > 0 {
		ctx, cancel = context.WithTimeout(ctx, captureDuration)
		defer cancel()
	}

	interrupted := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			cancel()
			close(interrupted)
		case <-ctx.Done():
		}
	}()

	operationTimeout := GetTimeout()

	// Create ephemeral container.
	createCtx, createCancel := context.WithTimeout(ctx, operationTimeout)
	defer createCancel()

	containerName, err := k8s.CreateEphemeralContainer(createCtx, client, k8s.EphemeralContainerOpts{
		PodName:         podName,
		Namespace:       namespace,
		Image:           GetDebugImage(),
		Command:         tcpdumpCmd,
		Stdin:           false,
		TTY:             false,
		ImagePullSecret: GetImagePullSecret(),
	})
	if err != nil {
		return fmt.Errorf("failed to create ephemeral container: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[verbose] ephemeral container name: %s\n", containerName)
	}

	// Wait for container to be running.
	waitCtx, waitCancel := context.WithTimeout(ctx, operationTimeout)
	defer waitCancel()

	if err := k8s.WaitForContainerRunning(waitCtx, client, namespace, podName, containerName); err != nil {
		return fmt.Errorf("failed waiting for capture container: %w", err)
	}

	// Prepare output destination.
	var outputWriter *os.File
	if captureOutput != "" {
		f, err := os.OpenFile(captureOutput, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("failed to create output file %q: %w", captureOutput, err)
		}
		defer f.Close()
		outputWriter = f
	} else {
		outputWriter = os.Stdout
	}

	// Attach to the container — tcpdump is already running as the container's
	// entrypoint (set via Command in EphemeralContainerOpts), so we attach to
	// its stdout/stderr stream rather than exec-ing a second process.
	execErr := k8s.AttachToContainer(ctx, client, k8s.AttachOpts{
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
		Stdin:         nil,
		Stdout:        outputWriter,
		Stderr:        os.Stderr,
		TTY:           false,
	})

	// Determine whether we were interrupted.
	select {
	case <-interrupted:
		fmt.Fprintln(os.Stderr, "Capture interrupted.")
		if captureOutput != "" {
			fmt.Fprintf(os.Stderr, "Partial capture written to %s\n", captureOutput)
		}
		return nil
	default:
	}

	if execErr != nil {
		// Context deadline exceeded from duration flag is a clean stop.
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "Capture complete.")
			if captureOutput != "" {
				fmt.Fprintf(os.Stderr, "Capture written to %s\n", captureOutput)
			}
			return nil
		}
		return fmt.Errorf("capture failed: %w", execErr)
	}

	fmt.Fprintln(os.Stderr, "Capture complete.")
	if captureOutput != "" {
		fmt.Fprintf(os.Stderr, "Capture written to %s\n", captureOutput)
	}
	return nil
}
