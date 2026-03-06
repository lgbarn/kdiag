package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

// NodeDebugOpts holds the parameters for creating a node debug pod.
type NodeDebugOpts struct {
	NodeName        string
	Namespace       string
	Image           string
	ImagePullSecret string // optional
}

// CreateNodeDebugPod creates a privileged debug pod scheduled directly on the
// specified node, with host PID/network/IPC namespaces and the host filesystem
// mounted at /host. Returns the generated pod name.
func CreateNodeDebugPod(ctx context.Context, client *Client, opts NodeDebugOpts) (string, error) {
	raw := fmt.Sprintf("kdiag-node-%s-%s", opts.NodeName, utilrand.String(5))
	podName := raw
	if len(podName) > 63 {
		podName = podName[:63]
	}

	hostPathType := corev1.HostPathDirectory

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: opts.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kdiag",
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      opts.NodeName,
			RestartPolicy: corev1.RestartPolicyNever,
			HostPID:       true,
			HostNetwork:   true,
			HostIPC:       true,
			Tolerations: []corev1.Toleration{
				{
					Operator: corev1.TolerationOpExists,
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "host-root",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/",
							Type: &hostPathType,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "debugger",
					Image: opts.Image,
					Stdin: true,
					TTY:   true,
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(true),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host-root",
							MountPath: "/host",
						},
					},
				},
			},
		},
	}

	if opts.ImagePullSecret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: opts.ImagePullSecret},
		}
	}

	created, err := client.Clientset.CoreV1().Pods(opts.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create node debug pod %s/%s: %w", opts.Namespace, podName, err)
	}

	return created.Name, nil
}

// WaitForPodRunning watches the named pod until its phase is Running.
// It returns an error if the pod reaches the Failed phase or the context is cancelled.
func WaitForPodRunning(ctx context.Context, client *Client, namespace, podName string) error {
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

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod %s/%s failed: %s", namespace, podName, pod.Status.Message)
		}

		return false, nil
	}

	_, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, conditionFunc)
	if err != nil {
		return fmt.Errorf("error waiting for pod %s/%s to reach Running state: %w", namespace, podName, err)
	}

	return nil
}

// DeleteNodeDebugPod deletes the named pod from the given namespace.
func DeleteNodeDebugPod(ctx context.Context, client *Client, namespace, podName string) error {
	err := client.Clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete node debug pod %s/%s: %w", namespace, podName, err)
	}
	return nil
}
