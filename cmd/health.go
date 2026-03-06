package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// ErrHealthCritical is returned by runHealth when the cluster has critical issues.
// main.go detects this sentinel to suppress the error message (issues are already
// printed in the health report table) while still setting exit code 1.
var ErrHealthCritical = errors.New("health: critical issues found")

// HealthReport is the top-level structured result for --output json.
type HealthReport struct {
	Nodes       []NodeSummary       `json:"nodes"`
	PodIssues   []PodIssueSummary   `json:"pod_issues"`
	Controllers []ControllerSummary `json:"controllers"`
	Events      []EventSummary       `json:"events"`
	Critical    bool                `json:"critical"`
}

// NodeSummary describes the health state of a single node.
type NodeSummary struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Ready          bool   `json:"ready"`
	MemoryPressure bool   `json:"memory_pressure"`
	DiskPressure   bool   `json:"disk_pressure"`
	PIDPressure    bool   `json:"pid_pressure"`
}

// PodIssueSummary describes a pod that is not in a healthy state.
type PodIssueSummary struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Reason    string `json:"reason"`
}

// ControllerSummary describes an unhealthy controller workload.
type ControllerSummary struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Ready     int32  `json:"ready"`
	Desired   int32  `json:"desired"`
	Status    string `json:"status"`
}

// EventSummary holds a condensed view of a Kubernetes event.
// Shared by health.go and inspect.go (same package); keep fields in sync if modified.
type EventSummary struct {
	Namespace string `json:"namespace"`
	Object    string `json:"object,omitempty"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Count     int32  `json:"count"`
	Age       string `json:"age"`
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Cluster-wide health report: nodes, pods, controllers, and warning events",
	Args:  cobra.NoArgs,
	RunE:  runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
}

// evaluateNode examines node conditions and returns a summary and whether it is critical.
func evaluateNode(node corev1.Node) (NodeSummary, bool) {
	summary := NodeSummary{
		Name:   node.Name,
		Status: "Ready",
		Ready:  true,
	}
	critical := false

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			if cond.Status != corev1.ConditionTrue {
				summary.Ready = false
				summary.Status = "NotReady"
				critical = true
			}
		case corev1.NodeMemoryPressure:
			if cond.Status == corev1.ConditionTrue {
				summary.MemoryPressure = true
				critical = true
			}
		case corev1.NodeDiskPressure:
			if cond.Status == corev1.ConditionTrue {
				summary.DiskPressure = true
				critical = true
			}
		case corev1.NodePIDPressure:
			if cond.Status == corev1.ConditionTrue {
				summary.PIDPressure = true
				// PID pressure is a warning, not critical per plan spec
			}
		}
	}

	return summary, critical
}

// evaluatePod checks if a pod is in an issue state and whether it is critical.
func evaluatePod(pod corev1.Pod) (PodIssueSummary, bool) {
	summary := PodIssueSummary{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		Phase:     string(pod.Status.Phase),
	}

	switch pod.Status.Phase {
	case corev1.PodFailed:
		summary.Reason = "Failed"
		return summary, true
	case corev1.PodPending:
		summary.Reason = "Pending"
		return summary, true
	case corev1.PodUnknown:
		summary.Reason = "Unknown"
		return summary, true
	case corev1.PodRunning:
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				summary.Reason = "CrashLoopBackOff"
				return summary, true
			}
		}
	}

	return summary, false
}

// evaluateDeployment checks deployment health and returns a summary and whether it is an issue.
func evaluateDeployment(deploy appsv1.Deployment) (ControllerSummary, bool) {
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	summary := ControllerSummary{
		Namespace: deploy.Namespace,
		Kind:      "Deployment",
		Name:      deploy.Name,
		Ready:     deploy.Status.ReadyReplicas,
		Desired:   desired,
		Status:    "Healthy",
	}

	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == "False" {
			summary.Status = "Unavailable"
			return summary, true
		}
		if cond.Type == appsv1.DeploymentProgressing && cond.Reason == "ProgressDeadlineExceeded" {
			summary.Status = "DeadlineExceeded"
			return summary, true
		}
	}

	if deploy.Status.ReadyReplicas < desired {
		summary.Status = "Degraded"
		return summary, true
	}

	return summary, false
}

// evaluateDaemonSet checks daemonset health and returns a summary and whether it is an issue.
func evaluateDaemonSet(ds appsv1.DaemonSet) (ControllerSummary, bool) {
	summary := ControllerSummary{
		Namespace: ds.Namespace,
		Kind:      "DaemonSet",
		Name:      ds.Name,
		Ready:     ds.Status.NumberReady,
		Desired:   ds.Status.DesiredNumberScheduled,
		Status:    "Healthy",
	}

	if ds.Status.NumberReady == 0 && ds.Status.DesiredNumberScheduled > 0 {
		summary.Status = "Unavailable"
		return summary, true
	}

	if ds.Status.NumberReady < ds.Status.DesiredNumberScheduled {
		summary.Status = "Degraded"
		return summary, true
	}

	return summary, false
}

// evaluateStatefulSet checks statefulset health and returns a summary and whether it is an issue.
func evaluateStatefulSet(ss appsv1.StatefulSet) (ControllerSummary, bool) {
	desired := int32(1)
	if ss.Spec.Replicas != nil {
		desired = *ss.Spec.Replicas
	}

	summary := ControllerSummary{
		Namespace: ss.Namespace,
		Kind:      "StatefulSet",
		Name:      ss.Name,
		Ready:     ss.Status.ReadyReplicas,
		Desired:   desired,
		Status:    "Healthy",
	}

	if ss.Status.ReadyReplicas == 0 && desired > 0 {
		summary.Status = "Unavailable"
		return summary, true
	}

	if ss.Status.ReadyReplicas < desired {
		summary.Status = "Degraded"
		return summary, true
	}

	return summary, false
}

func runHealth(cmd *cobra.Command, args []string) error {
	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	// --- Fetch all cluster-wide data ---

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing all nodes\n")
	}
	nodeList, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing all pods across all namespaces\n")
	}
	podList, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing all deployments across all namespaces\n")
	}
	deployList, err := client.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing all daemonsets across all namespaces\n")
	}
	dsList, err := client.Clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list daemonsets: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing all statefulsets across all namespaces\n")
	}
	ssList, err := client.Clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list statefulsets: %w", err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing warning events across all namespaces\n")
	}
	eventList, err := client.Clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{
		FieldSelector: "type=Warning",
		Limit:         100,
	})
	if err != nil {
		return fmt.Errorf("failed to list events: %w", err)
	}

	// --- Evaluate nodes ---
	critical := false
	nodeSummaries := make([]NodeSummary, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		ns, isCritical := evaluateNode(node)
		nodeSummaries = append(nodeSummaries, ns)
		if isCritical {
			critical = true
		}
	}

	// --- Evaluate pods ---
	podIssues := make([]PodIssueSummary, 0)
	for _, pod := range podList.Items {
		pi, isIssue := evaluatePod(pod)
		if isIssue {
			podIssues = append(podIssues, pi)
			if pi.Reason == "Failed" || pi.Reason == "CrashLoopBackOff" {
				critical = true
			}
		}
	}

	// --- Evaluate controllers ---
	controllerIssues := make([]ControllerSummary, 0)
	for _, deploy := range deployList.Items {
		cs, isIssue := evaluateDeployment(deploy)
		if isIssue {
			controllerIssues = append(controllerIssues, cs)
			if cs.Status == "Unavailable" || cs.Status == "DeadlineExceeded" {
				critical = true
			}
		}
	}
	for _, ds := range dsList.Items {
		cs, isIssue := evaluateDaemonSet(ds)
		if isIssue {
			controllerIssues = append(controllerIssues, cs)
			if cs.Status == "Unavailable" {
				critical = true
			}
		}
	}
	for _, ss := range ssList.Items {
		cs, isIssue := evaluateStatefulSet(ss)
		if isIssue {
			controllerIssues = append(controllerIssues, cs)
			if cs.Status == "Unavailable" {
				critical = true
			}
		}
	}

	// --- Format events (up to 20 most recent warning events) ---
	// eventList.Items are returned in API order; take up to 20.
	limit := len(eventList.Items)
	if limit > 20 {
		limit = 20
	}
	eventSummaries := make([]EventSummary, 0, limit)
	for _, ev := range eventList.Items[:limit] {
		eventSummaries = append(eventSummaries, EventSummary{
			Namespace: ev.Namespace,
			Object:    ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
			Type:      ev.Type,
			Reason:    ev.Reason,
			Message:   ev.Message,
			Count:     ev.Count,
			Age:       eventAge(ev.LastTimestamp, ev.EventTime),
		})
	}

	// --- Build report ---
	report := HealthReport{
		Nodes:       nodeSummaries,
		PodIssues:   podIssues,
		Controllers: controllerIssues,
		Events:      eventSummaries,
		Critical:    critical,
	}

	// --- Output ---
	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		if err := jp.Print(report); err != nil {
			return err
		}
		if critical {
			return ErrHealthCritical
		}
		return nil
	}

	// --- Table output ---

	// Section 1: Node Health
	fmt.Fprintf(os.Stdout, "Node Health\n")
	printer.PrintHeader("NAME", "STATUS", "READY", "MEM_PRESSURE", "DISK_PRESSURE", "PID_PRESSURE")
	for _, n := range nodeSummaries {
		printer.PrintRow(
			n.Name,
			n.Status,
			boolStr(n.Ready),
			boolStr(n.MemoryPressure),
			boolStr(n.DiskPressure),
			boolStr(n.PIDPressure),
		)
	}
	if err := printer.Flush(); err != nil {
		return err
	}

	// Section 2: Pods with Issues
	if len(podIssues) > 0 {
		fmt.Fprintf(os.Stdout, "\nPods with Issues\n")
		printer.PrintHeader("NAMESPACE", "NAME", "PHASE", "REASON")
		for _, pi := range podIssues {
			printer.PrintRow(pi.Namespace, pi.Name, pi.Phase, pi.Reason)
		}
		if err := printer.Flush(); err != nil {
			return err
		}
	}

	// Section 3: Controller Health Issues
	if len(controllerIssues) > 0 {
		fmt.Fprintf(os.Stdout, "\nController Health Issues\n")
		printer.PrintHeader("NAMESPACE", "KIND", "NAME", "READY", "DESIRED", "STATUS")
		for _, ci := range controllerIssues {
			printer.PrintRow(
				ci.Namespace,
				ci.Kind,
				ci.Name,
				fmt.Sprintf("%d", ci.Ready),
				fmt.Sprintf("%d", ci.Desired),
				ci.Status,
			)
		}
		if err := printer.Flush(); err != nil {
			return err
		}
	}

	// Section 4: Recent Warning Events
	if len(eventSummaries) > 0 {
		fmt.Fprintf(os.Stdout, "\nRecent Warning Events (showing up to 20)\n")
		printer.PrintHeader("NAMESPACE", "OBJECT", "REASON", "MESSAGE", "COUNT", "AGE")
		for _, ev := range eventSummaries {
			printer.PrintRow(
				ev.Namespace,
				ev.Object,
				ev.Reason,
				ev.Message,
				fmt.Sprintf("%d", ev.Count),
				ev.Age,
			)
		}
		if err := printer.Flush(); err != nil {
			return err
		}
	}

	// Summary line
	fmt.Fprintf(os.Stdout, "\nSummary: %d nodes, %d pod issues, %d controller issues, %d warning events\n",
		len(nodeSummaries), len(podIssues), len(controllerIssues), len(eventSummaries))
	if critical {
		fmt.Fprintf(os.Stdout, "Status: CRITICAL\n")
	} else {
		fmt.Fprintf(os.Stdout, "Status: OK\n")
	}

	if critical {
		return ErrHealthCritical
	}
	return nil
}

// boolStr converts a bool to "true"/"false" string for table output.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
