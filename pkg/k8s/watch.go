package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// ListPodsBySelector returns all pods in namespace that match the given label selector.
func ListPodsBySelector(ctx context.Context, client *Client, namespace, labelSelector string) ([]corev1.Pod, error) {
	podList, err := client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods with selector %q in namespace %q: %w", labelSelector, namespace, err)
	}
	return podList.Items, nil
}

// ListEvents returns events in namespace filtered by involvedObject kind and name.
// If kind is empty, the field selector is built without the kind constraint.
func ListEvents(ctx context.Context, client *Client, namespace, kind, name string) ([]corev1.Event, error) {
	fs := fields.Set{
		"involvedObject.name":      name,
		"involvedObject.namespace": namespace,
	}
	if kind != "" {
		fs["involvedObject.kind"] = kind
	}
	fieldSelector := fs.String()

	eventList, err := client.Clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list events for %s/%s in namespace %q: %w", kind, name, namespace, err)
	}
	return eventList.Items, nil
}

// StreamPodLogs streams logs from the specified pod (and optionally container) to w.
// It follows the log stream and includes timestamps. Blocks until the stream is closed
// or ctx is cancelled.
func StreamPodLogs(ctx context.Context, client *Client, namespace, podName, containerName string, w io.Writer) error {
	opts := &corev1.PodLogOptions{
		Follow:     true,
		Timestamps: true,
	}
	if containerName != "" {
		opts.Container = containerName
	}

	stream, err := client.Clientset.CoreV1().Pods(namespace).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to open log stream for pod %s/%s: %w", namespace, podName, err)
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		if _, err := fmt.Fprintln(w, scanner.Text()); err != nil {
			return err
		}
	}
	return scanner.Err()
}
