package k8s

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

// EphemeralContainerOpts holds the parameters for injecting an ephemeral container into a pod.
type EphemeralContainerOpts struct {
	PodName         string
	Namespace       string
	Image           string
	Command         []string // nil means interactive shell (no command override)
	Stdin           bool
	TTY             bool
	ImagePullSecret string // optional
}

// CreateEphemeralContainer injects a new ephemeral container into the specified pod and
// returns the generated container name. The caller should then call WaitForContainerRunning
// before attempting to attach or exec.
func CreateEphemeralContainer(ctx context.Context, client *Client, opts EphemeralContainerOpts) (string, error) {
	containerName := fmt.Sprintf("kdiag-%s", utilrand.String(5))

	pod, err := client.Clientset.CoreV1().Pods(opts.Namespace).Get(ctx, opts.PodName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod %s/%s: %w", opts.Namespace, opts.PodName, err)
	}

	ec := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    containerName,
			Image:   opts.Image,
			Stdin:   opts.Stdin,
			TTY:     opts.TTY,
			Command: opts.Command,
		},
	}

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)

	_, err = client.Clientset.CoreV1().Pods(opts.Namespace).UpdateEphemeralContainers(
		ctx, pod.Name, pod, metav1.UpdateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to update ephemeral containers for pod %s/%s: %w", opts.Namespace, opts.PodName, err)
	}

	return containerName, nil
}

// WaitForContainerRunning watches the pod until the named ephemeral container reaches the
// Running state. It returns an error if the container terminates or enters a waiting state
// with a failure reason, or if the context is cancelled.
func WaitForContainerRunning(ctx context.Context, client *Client, namespace, podName, containerName string) error {
	fieldSel := fields.OneTermEqualSelector("metadata.name", podName).String()

	lw := cache.NewListWatchFromClient(
		client.Clientset.CoreV1().RESTClient(),
		"pods",
		namespace,
		fields.ParseSelectorOrDie(fieldSel),
	)

	conditionFunc := func(event watch.Event) (bool, error) {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			return false, nil
		}

		for _, cs := range pod.Status.EphemeralContainerStatuses {
			if cs.Name != containerName {
				continue
			}

			if cs.State.Running != nil {
				return true, nil
			}

			if cs.State.Terminated != nil {
				t := cs.State.Terminated
				return false, fmt.Errorf("ephemeral container %q terminated: reason=%s exitCode=%d", containerName, t.Reason, t.ExitCode)
			}

			if cs.State.Waiting != nil {
				w := cs.State.Waiting
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" || w.Reason == "CreateContainerError" || w.Reason == "CreateContainerConfigError" {
					return false, fmt.Errorf("ephemeral container %q failed to start: reason=%s message=%s", containerName, w.Reason, w.Message)
				}
			}
		}

		return false, nil
	}

	_, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, conditionFunc)
	if err != nil {
		return fmt.Errorf("error waiting for ephemeral container %q to start in pod %s/%s: %w", containerName, namespace, podName, err)
	}

	return nil
}

// EphemeralExecOpts holds the parameters for RunInEphemeralContainer.
type EphemeralExecOpts struct {
	PodName         string
	Namespace       string
	Image           string
	ImagePullSecret string
	Command         []string // command to exec inside the container (not the container entrypoint)
	Stdout          io.Writer
	Stderr          io.Writer
	Verbose         bool
}

// RunInEphemeralContainer encapsulates the common 5-step ephemeral container
// exec pattern used by the dns and connectivity commands:
//  1. Run RBAC pre-flight (CheckEphemeralContainerRBAC + FormatRBACError).
//  2. Create an ephemeral container with entrypoint "sleep infinity".
//  3. Wait for the container to reach Running state.
//  4. Exec opts.Command inside the container, streaming to opts.Stdout/Stderr.
//  5. Return any exec error.
//
// capture.go is intentionally excluded because it uses attach instead of exec
// and requires special SIGINT handling and file output.
func RunInEphemeralContainer(ctx context.Context, client *Client, opts EphemeralExecOpts) error {
	// Step 1: RBAC pre-flight.
	checks, err := CheckEphemeralContainerRBAC(ctx, client.Clientset, opts.Namespace)
	if err != nil {
		return fmt.Errorf("error checking RBAC: %w", err)
	}
	if msg := FormatRBACError(checks); msg != "" {
		return fmt.Errorf("insufficient permissions to use ephemeral containers\n\n%s", msg)
	}

	// Step 2: Create ephemeral container (sleep keeps it alive for exec).
	containerName, err := CreateEphemeralContainer(ctx, client, EphemeralContainerOpts{
		PodName:         opts.PodName,
		Namespace:       opts.Namespace,
		Image:           opts.Image,
		Command:         []string{"sleep", "infinity"},
		Stdin:           false,
		TTY:             false,
		ImagePullSecret: opts.ImagePullSecret,
	})
	if err != nil {
		return fmt.Errorf("error creating ephemeral container: %w", err)
	}

	// Step 3: Wait for running.
	if err := WaitForContainerRunning(ctx, client, opts.Namespace, opts.PodName, containerName); err != nil {
		return fmt.Errorf("error waiting for ephemeral container to start: %w", err)
	}

	// Step 4: Exec the requested command.
	return ExecInContainer(ctx, client, ExecOpts{
		Namespace:     opts.Namespace,
		PodName:       opts.PodName,
		ContainerName: containerName,
		Command:       opts.Command,
		Stdin:         nil,
		Stdout:        opts.Stdout,
		Stderr:        opts.Stderr,
		TTY:           false,
	})
}
