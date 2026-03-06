package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// InspectResult holds enriched resource details for the inspect command.
type InspectResult struct {
	ResourceKind string             `json:"resource_kind"`
	ResourceName string             `json:"resource_name"`
	Namespace    string             `json:"namespace"`
	OwnerChain   []OwnerChainEntry  `json:"owner_chain"`
	Conditions   []ConditionSummary `json:"conditions"`
	Containers   []ContainerSummary `json:"containers,omitempty"`
	Replicas     *ReplicaSummary    `json:"replicas,omitempty"`
	Events       []EventSummary     `json:"events"`
}

// OwnerChainEntry represents one step in the resource ownership hierarchy.
type OwnerChainEntry struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ConditionSummary holds a condensed view of a Kubernetes condition.
type ConditionSummary struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ContainerSummary holds per-container status information for pods.
type ContainerSummary struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	State        string `json:"state"`
	StateDetail  string `json:"state_detail,omitempty"`
}

// ReplicaSummary holds replica counts for workload resources.
type ReplicaSummary struct {
	Desired   int32 `json:"desired"`
	Ready     int32 `json:"ready"`
	Available int32 `json:"available"`
	Updated   int32 `json:"updated"`
}

var inspectCmd = &cobra.Command{
	Use:   "inspect <type/name>",
	Short: "Show enriched resource details: owner chain, events, conditions, and container status",
	Long:  "Inspect a Kubernetes resource with enriched output including ownership chain, related events, conditions, restart counts, and container statuses. Supports: pod, deployment, replicaset, daemonset, statefulset.",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}

// parseResourceArg parses a "type/name" argument into its kind and name components.
func parseResourceArg(arg string) (kind, name string, err error) {
	parts := strings.SplitN(arg, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid resource argument %q: expected type/name format (e.g., pod/nginx, deployment/myapp)", arg)
	}
	kind = strings.ToLower(parts[0])
	name = parts[1]

	supported := map[string]bool{
		"pod":         true,
		"deployment":  true,
		"replicaset":  true,
		"daemonset":   true,
		"statefulset": true,
	}
	if !supported[kind] {
		return "", "", fmt.Errorf("unsupported resource type %q: supported types are pod, deployment, replicaset, daemonset, statefulset", kind)
	}
	return kind, name, nil
}

func runInspect(cmd *cobra.Command, args []string) error {
	kind, name, err := parseResourceArg(args[0])
	if err != nil {
		return err
	}

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	var result *InspectResult

	switch kind {
	case "pod":
		result, err = inspectPod(ctx, client, namespace, name)
	case "deployment":
		result, err = inspectDeployment(ctx, client, namespace, name)
	case "replicaset":
		result, err = inspectReplicaSet(ctx, client, namespace, name)
	case "daemonset":
		result, err = inspectDaemonSet(ctx, client, namespace, name)
	case "statefulset":
		result, err = inspectStatefulSet(ctx, client, namespace, name)
	}
	if err != nil {
		return err
	}

	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	return printInspectTable(result)
}

func inspectPod(ctx context.Context, client *k8s.Client, namespace, name string) (*InspectResult, error) {
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting pod %q in namespace %q\n", name, namespace)
	}

	pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("pod %q not found in namespace %q (hint: kubectl get pods -n %s)", name, namespace, namespace)
		}
		return nil, fmt.Errorf("failed to get pod %q: %w", name, err)
	}

	// Build owner chain starting with the pod itself.
	chain := []OwnerChainEntry{{Kind: "Pod", Name: name}}
ownerLoop:
	for _, ref := range pod.OwnerReferences {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		switch ref.Kind {
		case "ReplicaSet":
			chain = append(chain, OwnerChainEntry{Kind: "ReplicaSet", Name: ref.Name})
			// Try to find a Deployment owner of the ReplicaSet.
			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] traversing owner chain: getting replicaset %q\n", ref.Name)
			}
			rs, rsErr := client.Clientset.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if rsErr != nil {
				if IsVerbose() {
					fmt.Fprintf(os.Stderr, "[kdiag] warning: could not get replicaset %q: %v\n", ref.Name, rsErr)
				}
				break ownerLoop
			}
			for _, deployRef := range rs.OwnerReferences {
				if deployRef.Controller == nil || !*deployRef.Controller {
					continue
				}
				if deployRef.Kind == "Deployment" {
					chain = append(chain, OwnerChainEntry{Kind: "Deployment", Name: deployRef.Name})
					break
				}
			}
			break ownerLoop
		case "DaemonSet":
			chain = append(chain, OwnerChainEntry{Kind: "DaemonSet", Name: ref.Name})
			break ownerLoop
		case "StatefulSet":
			chain = append(chain, OwnerChainEntry{Kind: "StatefulSet", Name: ref.Name})
			break ownerLoop
		case "Job":
			chain = append(chain, OwnerChainEntry{Kind: "Job", Name: ref.Name})
			break ownerLoop
		default:
			chain = append(chain, OwnerChainEntry{Kind: ref.Kind, Name: ref.Name})
			break ownerLoop
		}
	}

	// Build conditions.
	conditions := make([]ConditionSummary, 0, len(pod.Status.Conditions))
	for _, c := range pod.Status.Conditions {
		conditions = append(conditions, ConditionSummary{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	// Build container statuses.
	containers := make([]ContainerSummary, 0, len(pod.Status.ContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		state := "Unknown"
		stateDetail := ""
		switch {
		case cs.State.Running != nil:
			state = "Running"
		case cs.State.Waiting != nil:
			state = "Waiting"
			stateDetail = cs.State.Waiting.Reason
		case cs.State.Terminated != nil:
			state = "Terminated"
			stateDetail = cs.State.Terminated.Reason
		}
		containers = append(containers, ContainerSummary{
			Name:         cs.Name,
			Ready:        cs.Ready,
			RestartCount: cs.RestartCount,
			State:        state,
			StateDetail:  stateDetail,
		})
	}

	// Get events.
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing events for pod %q\n", name)
	}
	rawEvents, err := k8s.ListEvents(ctx, client, namespace, "Pod", name)
	if err != nil {
		return nil, err
	}
	events := summarizeEvents(rawEvents)

	return &InspectResult{
		ResourceKind: "Pod",
		ResourceName: name,
		Namespace:    namespace,
		OwnerChain:   chain,
		Conditions:   conditions,
		Containers:   containers,
		Replicas:     nil,
		Events:       events,
	}, nil
}

func inspectDeployment(ctx context.Context, client *k8s.Client, namespace, name string) (*InspectResult, error) {
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting deployment %q in namespace %q\n", name, namespace)
	}

	deploy, err := client.Clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("deployment %q not found in namespace %q (hint: kubectl get deployments -n %s)", name, namespace, namespace)
		}
		return nil, fmt.Errorf("failed to get deployment %q: %w", name, err)
	}

	chain := []OwnerChainEntry{{Kind: "Deployment", Name: name}}

	conditions := make([]ConditionSummary, 0, len(deploy.Status.Conditions))
	for _, c := range deploy.Status.Conditions {
		conditions = append(conditions, ConditionSummary{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	replicas := &ReplicaSummary{
		Desired:   derefReplicas(deploy.Spec.Replicas),
		Ready:     deploy.Status.ReadyReplicas,
		Available: deploy.Status.AvailableReplicas,
		Updated:   deploy.Status.UpdatedReplicas,
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing events for deployment %q\n", name)
	}
	rawEvents, err := k8s.ListEvents(ctx, client, namespace, "Deployment", name)
	if err != nil {
		return nil, err
	}
	events := summarizeEvents(rawEvents)

	return &InspectResult{
		ResourceKind: "Deployment",
		ResourceName: name,
		Namespace:    namespace,
		OwnerChain:   chain,
		Conditions:   conditions,
		Containers:   nil,
		Replicas:     replicas,
		Events:       events,
	}, nil
}

func inspectReplicaSet(ctx context.Context, client *k8s.Client, namespace, name string) (*InspectResult, error) {
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting replicaset %q in namespace %q\n", name, namespace)
	}

	rs, err := client.Clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("replicaset %q not found in namespace %q (hint: kubectl get replicasets -n %s)", name, namespace, namespace)
		}
		return nil, fmt.Errorf("failed to get replicaset %q: %w", name, err)
	}

	chain := []OwnerChainEntry{{Kind: "ReplicaSet", Name: name}}
	for _, ref := range rs.OwnerReferences {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		if ref.Kind == "Deployment" {
			chain = append(chain, OwnerChainEntry{Kind: "Deployment", Name: ref.Name})
			break
		}
	}

	conditions := make([]ConditionSummary, 0, len(rs.Status.Conditions))
	for _, c := range rs.Status.Conditions {
		conditions = append(conditions, ConditionSummary{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	replicas := &ReplicaSummary{
		Desired:   derefReplicas(rs.Spec.Replicas),
		Ready:     rs.Status.ReadyReplicas,
		Available: rs.Status.AvailableReplicas,
		Updated:   0, // ReplicaSet does not track updated replicas
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing events for replicaset %q\n", name)
	}
	rawEvents, err := k8s.ListEvents(ctx, client, namespace, "ReplicaSet", name)
	if err != nil {
		return nil, err
	}
	events := summarizeEvents(rawEvents)

	return &InspectResult{
		ResourceKind: "ReplicaSet",
		ResourceName: name,
		Namespace:    namespace,
		OwnerChain:   chain,
		Conditions:   conditions,
		Replicas:     replicas,
		Events:       events,
	}, nil
}

func inspectDaemonSet(ctx context.Context, client *k8s.Client, namespace, name string) (*InspectResult, error) {
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting daemonset %q in namespace %q\n", name, namespace)
	}

	ds, err := client.Clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("daemonset %q not found in namespace %q (hint: kubectl get daemonsets -n %s)", name, namespace, namespace)
		}
		return nil, fmt.Errorf("failed to get daemonset %q: %w", name, err)
	}

	chain := []OwnerChainEntry{{Kind: "DaemonSet", Name: name}}

	// DaemonSets do not have standard conditions; start with empty slice.
	conditions := []ConditionSummary{}

	replicas := &ReplicaSummary{
		Desired:   ds.Status.DesiredNumberScheduled,
		Ready:     ds.Status.NumberReady,
		Available: ds.Status.NumberAvailable,
		Updated:   ds.Status.UpdatedNumberScheduled,
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing events for daemonset %q\n", name)
	}
	rawEvents, err := k8s.ListEvents(ctx, client, namespace, "DaemonSet", name)
	if err != nil {
		return nil, err
	}
	events := summarizeEvents(rawEvents)

	return &InspectResult{
		ResourceKind: "DaemonSet",
		ResourceName: name,
		Namespace:    namespace,
		OwnerChain:   chain,
		Conditions:   conditions,
		Replicas:     replicas,
		Events:       events,
	}, nil
}

func inspectStatefulSet(ctx context.Context, client *k8s.Client, namespace, name string) (*InspectResult, error) {
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] getting statefulset %q in namespace %q\n", name, namespace)
	}

	ss, err := client.Clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("statefulset %q not found in namespace %q (hint: kubectl get statefulsets -n %s)", name, namespace, namespace)
		}
		return nil, fmt.Errorf("failed to get statefulset %q: %w", name, err)
	}

	chain := []OwnerChainEntry{{Kind: "StatefulSet", Name: name}}

	// StatefulSet conditions (may be empty in older K8s versions).
	conditions := make([]ConditionSummary, 0, len(ss.Status.Conditions))
	for _, c := range ss.Status.Conditions {
		conditions = append(conditions, ConditionSummary{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	replicas := &ReplicaSummary{
		Desired:   derefReplicas(ss.Spec.Replicas),
		Ready:     ss.Status.ReadyReplicas,
		Available: ss.Status.AvailableReplicas,
		Updated:   ss.Status.UpdatedReplicas,
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing events for statefulset %q\n", name)
	}
	rawEvents, err := k8s.ListEvents(ctx, client, namespace, "StatefulSet", name)
	if err != nil {
		return nil, err
	}
	events := summarizeEvents(rawEvents)

	return &InspectResult{
		ResourceKind: "StatefulSet",
		ResourceName: name,
		Namespace:    namespace,
		OwnerChain:   chain,
		Conditions:   conditions,
		Replicas:     replicas,
		Events:       events,
	}, nil
}

// summarizeEvents converts raw Kubernetes events into EventSummary values.
// EventSummary is defined in health.go and shared across the cmd package.
func summarizeEvents(rawEvents []corev1.Event) []EventSummary {
	events := make([]EventSummary, 0, len(rawEvents))
	for _, e := range rawEvents {
		events = append(events, EventSummary{
			Namespace: e.Namespace,
			Type:      e.Type,
			Reason:    e.Reason,
			Message:   e.Message,
			Count:     e.Count,
			Age:       eventAge(e.LastTimestamp, e.EventTime),
		})
	}
	return events
}

// printInspectTable renders the InspectResult as structured human-readable table output.
func printInspectTable(result *InspectResult) error {
	fmt.Fprintf(os.Stdout, "Resource: %s/%s\n", result.ResourceKind, result.ResourceName)
	fmt.Fprintf(os.Stdout, "Namespace: %s\n\n", result.Namespace)

	// Owner chain.
	chainParts := make([]string, 0, len(result.OwnerChain))
	for _, entry := range result.OwnerChain {
		chainParts = append(chainParts, fmt.Sprintf("%s/%s", entry.Kind, entry.Name))
	}
	fmt.Fprintf(os.Stdout, "Owner Chain: %s\n\n", strings.Join(chainParts, " -> "))

	// Conditions.
	fmt.Fprintf(os.Stdout, "Conditions:\n")
	condPrinter, err := output.NewPrinter("table", os.Stdout)
	if err != nil {
		return err
	}
	condPrinter.PrintHeader("  TYPE", "STATUS", "REASON", "MESSAGE")
	for _, c := range result.Conditions {
		condPrinter.PrintRow("  "+c.Type, c.Status, c.Reason, c.Message)
	}
	if err := condPrinter.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout)

	// Containers (pods only).
	if len(result.Containers) > 0 {
		fmt.Fprintf(os.Stdout, "Containers:\n")
		contPrinter, err := output.NewPrinter("table", os.Stdout)
		if err != nil {
			return err
		}
		contPrinter.PrintHeader("  NAME", "READY", "RESTARTS", "STATE", "DETAIL")
		for _, c := range result.Containers {
			contPrinter.PrintRow(
				"  "+c.Name,
				fmt.Sprintf("%v", c.Ready),
				fmt.Sprintf("%d", c.RestartCount),
				c.State,
				c.StateDetail,
			)
		}
		if err := contPrinter.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout)
	}

	// Replicas (deployments, daemonsets, statefulsets).
	if result.Replicas != nil {
		fmt.Fprintf(os.Stdout, "Replicas:\n")
		repPrinter, err := output.NewPrinter("table", os.Stdout)
		if err != nil {
			return err
		}
		repPrinter.PrintHeader("  DESIRED", "READY", "AVAILABLE", "UPDATED")
		repPrinter.PrintRow(
			fmt.Sprintf("  %d", result.Replicas.Desired),
			fmt.Sprintf("%d", result.Replicas.Ready),
			fmt.Sprintf("%d", result.Replicas.Available),
			fmt.Sprintf("%d", result.Replicas.Updated),
		)
		if err := repPrinter.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout)
	}

	// Events.
	fmt.Fprintf(os.Stdout, "Events:\n")
	evtPrinter, err := output.NewPrinter("table", os.Stdout)
	if err != nil {
		return err
	}
	evtPrinter.PrintHeader("  TYPE", "REASON", "MESSAGE", "COUNT", "AGE")
	for _, e := range result.Events {
		evtPrinter.PrintRow("  "+e.Type, e.Reason, e.Message, fmt.Sprintf("%d", e.Count), e.Age)
	}
	return evtPrinter.Flush()
}
