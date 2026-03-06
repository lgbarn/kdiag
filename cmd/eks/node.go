package eks

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	k8spkg "github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// NodeReport is the top-level JSON report for kdiag eks node.
type NodeReport struct {
	Nodes   []NodeENIStatus `json:"nodes"`
	Skipped []SkippedNode   `json:"skipped_nodes"`
	Summary NodeSummaryInfo `json:"summary"`
}

// NodeENIStatus holds per-node ENI/IP capacity details.
type NodeENIStatus struct {
	NodeName     string `json:"node_name"`
	InstanceType string `json:"instance_type"`
	ComputeType  string `json:"compute_type"`
	MaxENIs      int32  `json:"max_enis"`
	MaxIPsPerENI int32  `json:"max_ips_per_eni"`
	CurrentENIs  int    `json:"current_enis"`
	CurrentIPs   int    `json:"current_ips"`
	MaxTotalIPs  int    `json:"max_total_ips"`
	Utilization  string `json:"utilization_pct"`
	Status       string `json:"status"`
	Note         string `json:"note,omitempty"`
	Pods         *NodePodSummary `json:"pods,omitempty"`
}

// NodePodSummary holds pod details for a node when --show-pods is used.
type NodePodSummary struct {
	TotalPods    int                    `json:"total_pods"`
	DaemonSets   int                    `json:"daemonset_pods"`
	Workloads    int                    `json:"workload_pods"`
	ByNamespace  []NamespacePodCount    `json:"by_namespace"`
	DaemonSetPods []PodInfo             `json:"daemonset_pod_list,omitempty"`
	WorkloadPods  []PodInfo             `json:"workload_pod_list,omitempty"`
}

// NamespacePodCount holds pod count per namespace for a node.
type NamespacePodCount struct {
	Namespace string `json:"namespace"`
	Count     int    `json:"count"`
}

// PodInfo holds minimal pod details for the --show-pods output.
type PodInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	IP        string `json:"ip"`
	Status    string `json:"status"`
}

// NodeSummaryInfo holds aggregate counts for the report.
type NodeSummaryInfo struct {
	TotalNodes     int `json:"total_nodes"`
	CheckedNodes   int `json:"checked_nodes"`
	SkippedNodes   int `json:"skipped_nodes"`
	ExhaustedNodes int `json:"exhausted_nodes"`
}

var (
	showPods       bool
	statusFilter   string
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Show per-node ENI and IP capacity: instance type limits vs current allocation",
	Args:  cobra.NoArgs,
	RunE:  runNode,
}

func init() {
	nodeCmd.Flags().BoolVar(&showPods, "show-pods", false, "List pods on each node with daemonset/workload breakdown")
	nodeCmd.Flags().StringVar(&statusFilter, "status", "", "Only show nodes matching this status: EXHAUSTED, WARNING, or OK (requires --show-pods)")
	EksCmd.AddCommand(nodeCmd)
}

func runNode(cmd *cobra.Command, args []string) error {
	// 1. Build Kubernetes client and verify EKS.
	k8sClient, err := k8spkg.NewClient(configFlags)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	if err := requireEKS(k8sClient.Config.Host); err != nil {
		return err
	}

	// 2. Context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), getTimeout())
	defer cancel()

	// 3. EC2 client.
	ec2Client, err := newEC2Client(ctx, k8sClient.Config.Host)
	if err != nil {
		return fmt.Errorf("failed to create EC2 client: %w", err)
	}

	// 4. List all nodes.
	nodeList, err := k8sClient.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	var report NodeReport
	report.Summary.TotalNodes = len(nodeList.Items)

	// 5. Classify each node.
	eligible, skipped := ClassifyNodes(nodeList.Items)
	report.Skipped = append(report.Skipped, skipped...)
	uniqueTypes := map[string]struct{}{}
	for _, en := range eligible {
		uniqueTypes[en.InstanceType] = struct{}{}
	}

	// 7. Batch-query instance type limits.
	typeList := uniqueKeys(uniqueTypes)

	limitsMap, err := awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)
	if err != nil {
		return fmt.Errorf("failed to get instance type limits: %w", err)
	}

	// 8-9. Per-node ENI query and utilization calculation.
	for _, en := range eligible {
		eniInfo, err := awspkg.ListNodeENIs(ctx, ec2Client, en.InstanceID)
		if err != nil {
			if isVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] warning: could not list ENIs for node %s (%s): %v\n",
					en.Name, en.InstanceID, err)
			}
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: en.Name,
				Reason:   fmt.Sprintf("ENI query failed: %v", err),
			})
			continue
		}

		limits := limitsMap[en.InstanceType]
		if limits == nil {
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: en.Name,
				Reason:   fmt.Sprintf("instance type limits not available for %s", en.InstanceType),
			})
			continue
		}
		maxENIs := limits.MaxENIs
		maxIPsPerENI := limits.MaxIPsPerENI
		maxTotalIPs := int(maxENIs) * int(maxIPsPerENI)

		currentENIs := len(eniInfo.ENIs)
		currentIPs := eniInfo.TotalIPs

		utilPct := 0
		if maxTotalIPs > 0 {
			utilPct = (currentIPs * 100) / maxTotalIPs
		}

		status := "OK"
		if utilPct >= 85 {
			status = "EXHAUSTED"
		} else if utilPct >= 70 {
			status = "WARNING"
		}

		if status == "EXHAUSTED" {
			report.Summary.ExhaustedNodes++
		}

		report.Nodes = append(report.Nodes, NodeENIStatus{
			NodeName:     en.Name,
			InstanceType: en.InstanceType,
			ComputeType:  string(en.ComputeType),
			MaxENIs:      maxENIs,
			MaxIPsPerENI: maxIPsPerENI,
			CurrentENIs:  currentENIs,
			CurrentIPs:   currentIPs,
			MaxTotalIPs:  maxTotalIPs,
			Utilization:  strconv.Itoa(utilPct),
			Status:       status,
			Note:         en.Note,
		})
	}

	// 10. Populate summary.
	report.Summary.CheckedNodes = len(report.Nodes)
	report.Summary.SkippedNodes = len(report.Skipped)

	// 10b. Collect pod data when --show-pods is set.
	if showPods {
		if err := enrichNodesWithPods(ctx, k8sClient, &report); err != nil {
			return err
		}
	}

	// 10c. Filter by --status if set.
	if statusFilter != "" {
		filter := strings.ToUpper(statusFilter)
		filtered := make([]NodeENIStatus, 0, len(report.Nodes))
		for _, n := range report.Nodes {
			if n.Status == filter {
				filtered = append(filtered, n)
			}
		}
		report.Nodes = filtered
	}

	// 11. Output.
	printer, err := output.NewPrinter(getOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}
	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(report)
	}

	// Table output.
	p := output.NewTablePrinter(os.Stdout)
	p.PrintHeader("NODE", "INSTANCE_TYPE", "COMPUTE", "MAX_ENIS", "MAX_IPS/ENI", "ENIS", "IPS", "MAX_IPS", "UTIL%", "STATUS")
	for _, n := range report.Nodes {
		p.PrintRow(
			n.NodeName,
			n.InstanceType,
			n.ComputeType,
			strconv.Itoa(int(n.MaxENIs)),
			strconv.Itoa(int(n.MaxIPsPerENI)),
			strconv.Itoa(n.CurrentENIs),
			strconv.Itoa(n.CurrentIPs),
			strconv.Itoa(n.MaxTotalIPs),
			n.Utilization+"%",
			n.Status,
		)
	}
	if err := p.Flush(); err != nil {
		return err
	}

	// Show pod details per node when --show-pods is set.
	if showPods {
		for _, n := range report.Nodes {
			if n.Pods == nil {
				continue
			}
			fmt.Fprintf(os.Stdout, "\n--- %s (%s, %s%%, %s) — %d pods (%d daemonset, %d workload) ---\n",
				n.NodeName, n.InstanceType, n.Utilization, n.Status,
				n.Pods.TotalPods, n.Pods.DaemonSets, n.Pods.Workloads)

			// Namespace summary.
			nsParts := make([]string, 0, len(n.Pods.ByNamespace))
			for _, ns := range n.Pods.ByNamespace {
				nsParts = append(nsParts, fmt.Sprintf("%s:%d", ns.Namespace, ns.Count))
			}
			fmt.Fprintf(os.Stdout, "  Namespaces: %s\n", strings.Join(nsParts, ", "))

			if len(n.Pods.DaemonSetPods) > 0 {
				fmt.Fprintf(os.Stdout, "  DaemonSet pods:\n")
				for _, pod := range n.Pods.DaemonSetPods {
					fmt.Fprintf(os.Stdout, "    %-50s %-20s %s\n", pod.Namespace+"/"+pod.Name, pod.IP, pod.Status)
				}
			}
			if len(n.Pods.WorkloadPods) > 0 {
				fmt.Fprintf(os.Stdout, "  Workload pods:\n")
				for _, pod := range n.Pods.WorkloadPods {
					fmt.Fprintf(os.Stdout, "    %-50s %-20s %s\n", pod.Namespace+"/"+pod.Name, pod.IP, pod.Status)
				}
			}
		}
	}

	if err := printSkippedNodes(report.Skipped); err != nil {
		return err
	}

	atRisk := report.Summary.ExhaustedNodes
	warningCount := 0
	for _, n := range report.Nodes {
		if n.Status == "WARNING" {
			warningCount++
		}
	}

	_, _ = os.Stdout.WriteString(outgoingString(report.Summary.CheckedNodes, report.Summary.SkippedNodes, atRisk+warningCount))
	return nil
}

// enrichNodesWithPods queries all pods in the cluster and attaches pod summaries
// to each node in the report. Pods are classified as daemonset or workload based
// on their owner references.
func enrichNodesWithPods(ctx context.Context, client *k8spkg.Client, report *NodeReport) error {
	podList, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Index pods by node name.
	podsByNode := map[string][]corev1.Pod{}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != "" {
			podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], *pod)
		}
	}

	for i := range report.Nodes {
		node := &report.Nodes[i]
		pods := podsByNode[node.NodeName]

		summary := &NodePodSummary{}
		nsCounts := map[string]int{}

		for _, pod := range pods {
			summary.TotalPods++
			nsCounts[pod.Namespace]++

			info := PodInfo{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				IP:        pod.Status.PodIP,
				Status:    string(pod.Status.Phase),
			}

			if isDaemonSetPod(&pod) {
				summary.DaemonSets++
				summary.DaemonSetPods = append(summary.DaemonSetPods, info)
			} else {
				summary.Workloads++
				summary.WorkloadPods = append(summary.WorkloadPods, info)
			}
		}

		// Sort namespace counts by count descending.
		for ns, count := range nsCounts {
			summary.ByNamespace = append(summary.ByNamespace, NamespacePodCount{
				Namespace: ns,
				Count:     count,
			})
		}
		sort.Slice(summary.ByNamespace, func(a, b int) bool {
			return summary.ByNamespace[a].Count > summary.ByNamespace[b].Count
		})

		node.Pods = summary
	}
	return nil
}

// isDaemonSetPod returns true if the pod is owned by a DaemonSet.
func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
		// Pods owned by a Node (static pods like kube-proxy) are treated as daemonsets.
		if ref.Kind == "Node" {
			return true
		}
	}
	return false
}

// outgoingString formats the trailing summary line.
func outgoingString(checked, skipped, atRisk int) string {
	return fmt.Sprintf("\n%d nodes checked, %d skipped, %d at risk\n", checked, skipped, atRisk)
}
