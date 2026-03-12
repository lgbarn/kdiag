package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/lgbarn/kdiag/pkg/k8s"
)

var shellNodeName string

var shellCmd = &cobra.Command{
	Use:   "shell [pod-name]",
	Short: "Launch a debug shell in a pod or on a node",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runShell,
}

func init() {
	shellCmd.Flags().StringVar(&shellNodeName, "node", "", "Target node name for node-level debugging")
	rootCmd.AddCommand(shellCmd)
}

func runShell(cmd *cobra.Command, args []string) error {
	// Determine mode: pod shell vs. node shell
	nodeMode := cmd.Flags().Changed("node")

	// Validate: must have either pod name or --node
	if len(args) == 0 && !nodeMode {
		return fmt.Errorf("error: must specify a pod name or --node <node-name>\n\nUsage:\n  kdiag shell <pod-name>\n  kdiag shell --node <node-name>")
	}

	if nodeMode {
		return runNodeShell(cmd, args)
	}

	return runPodShell(cmd, args)
}

// runPodShell implements Path A: ephemeral container shell in a pod.
func runPodShell(cmd *cobra.Command, args []string) error {
	podName := StripPodPrefix(args[0])

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

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] fetching pod %s/%s\n", namespace, podName)
	}

	ctx := context.Background()

	pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("error: pod %q not found in namespace %q\n\nCheck the pod name with: kubectl get pods -n %s", podName, namespace, namespace)
		}
		if errors.IsForbidden(err) {
			return fmt.Errorf("error: forbidden — you do not have permission to get pod %q in namespace %q", podName, namespace)
		}
		return fmt.Errorf("error getting pod %q: %w", podName, err)
	}

	// Warn if Fargate — ephemeral containers may not be supported
	computeType := k8s.DetectComputeType(pod)
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] detected compute type: %s\n", computeType)
	}
	if computeType == k8s.ComputeTypeFargate {
		fmt.Fprintf(os.Stderr, "warning: pod %q appears to be running on Fargate — ephemeral containers may not be supported\n", podName)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] checking RBAC permissions\n")
	}

	// RBAC pre-flight check
	checks, err := k8s.CheckEphemeralContainerRBAC(ctx, client.Clientset, namespace)
	if err != nil {
		return fmt.Errorf("error checking RBAC: %w", err)
	}
	if msg := k8s.FormatRBACError(checks); msg != "" {
		return fmt.Errorf("insufficient permissions to use ephemeral containers\n\n%s", msg)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] creating ephemeral container in pod %s/%s\n", namespace, podName)
	}

	// Use timeout context for wait phases; background context for attach
	waitCtx, waitCancel := context.WithTimeout(ctx, GetTimeout())
	defer waitCancel()

	containerName, err := k8s.CreateEphemeralContainer(waitCtx, client, k8s.EphemeralContainerOpts{
		PodName:         podName,
		Namespace:       namespace,
		Image:           GetDebugImage(),
		Stdin:           true,
		TTY:             true,
		ImagePullSecret: GetImagePullSecret(),
	})
	if err != nil {
		if errors.IsForbidden(err) {
			rbacMsg := k8s.FormatRBACError(checks)
			if rbacMsg != "" {
				return fmt.Errorf("forbidden creating ephemeral container\n\n%s", rbacMsg)
			}
			return fmt.Errorf("error: forbidden creating ephemeral container in pod %q — check your RBAC permissions", podName)
		}
		return fmt.Errorf("error creating ephemeral container: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] waiting for container %q to start\n", containerName)
	}

	if err := k8s.WaitForContainerRunning(waitCtx, client, namespace, podName, containerName); err != nil {
		return fmt.Errorf("error waiting for ephemeral container to start: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] attaching to container %q\n", containerName)
	}

	// Attach with no timeout (user-controlled session)
	if err := k8s.AttachToContainer(ctx, client, k8s.AttachOpts{
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		TTY:           true,
	}); err != nil {
		return fmt.Errorf("error attaching to container: %w", err)
	}

	return nil
}

// runNodeShell implements Path B: privileged debug pod on a node.
func runNodeShell(cmd *cobra.Command, args []string) error {
	nodeName := shellNodeName

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
	ctx := context.Background()

	// Fetch the node from the API to validate it exists and detect compute type.
	node, err := client.Clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("error: node %q not found in the cluster", nodeName)
		}
		return fmt.Errorf("error getting node %q: %w", nodeName, err)
	}
	if k8s.DetectNodeComputeType(node) == k8s.ComputeTypeFargate {
		return fmt.Errorf("error: node %q is a Fargate virtual node — node-level debugging is not supported on Fargate\n\nFargate nodes do not support privileged pods or host-namespace access", nodeName)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] checking RBAC permissions for node shell\n")
	}

	// RBAC pre-flight: verify user can create pods and attach before attempting to create a privileged debug pod.
	canCreate, err := k8s.CheckSingleRBAC(ctx, client.Clientset, namespace, "create", "pods", "")
	if err != nil {
		return fmt.Errorf("error checking RBAC: %w", err)
	}
	canAttach, err := k8s.CheckSingleRBAC(ctx, client.Clientset, namespace, "create", "pods", "attach")
	if err != nil {
		return fmt.Errorf("error checking RBAC: %w", err)
	}
	if !canCreate || !canAttach {
		var missing []string
		if !canCreate {
			missing = append(missing, "pods/create")
		}
		if !canAttach {
			missing = append(missing, "pods/attach")
		}
		return fmt.Errorf("insufficient permissions in namespace %q: missing %s", namespace, strings.Join(missing, ", "))
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] creating node debug pod on node %q\n", nodeName)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, GetTimeout())
	defer waitCancel()

	podName, err := k8s.CreateNodeDebugPod(waitCtx, client, k8s.NodeDebugOpts{
		NodeName:        nodeName,
		Namespace:       namespace,
		Image:           GetDebugImage(),
		ImagePullSecret: GetImagePullSecret(),
	})
	if err != nil {
		return fmt.Errorf("error creating node debug pod: %w", err)
	}

	// Cleanup on exit with a bounded timeout.
	defer func() {
		fmt.Fprintf(os.Stderr, "[kdiag] cleaning up debug pod %q\n", podName)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if delErr := k8s.DeleteNodeDebugPod(cleanupCtx, client, namespace, podName); delErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete debug pod %q: %v\n", podName, delErr)
		}
	}()

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] waiting for debug pod %q to become ready\n", podName)
	}

	if err := k8s.WaitForPodRunning(waitCtx, client, namespace, podName); err != nil {
		return fmt.Errorf("error waiting for node debug pod to start: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] attaching to debug pod %q\n", podName)
	}

	// Attach with no timeout
	if err := k8s.AttachToContainer(ctx, client, k8s.AttachOpts{
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: "debugger",
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
		TTY:           true,
	}); err != nil {
		return fmt.Errorf("error attaching to node debug pod: %w", err)
	}

	return nil
}
