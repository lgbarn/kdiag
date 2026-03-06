package eks

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
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
}

// SkippedNode records a node that was excluded from the report with a reason.
type SkippedNode struct {
	NodeName string `json:"node_name"`
	Reason   string `json:"reason"`
}

// NodeSummaryInfo holds aggregate counts for the report.
type NodeSummaryInfo struct {
	TotalNodes     int `json:"total_nodes"`
	CheckedNodes   int `json:"checked_nodes"`
	SkippedNodes   int `json:"skipped_nodes"`
	ExhaustedNodes int `json:"exhausted_nodes"`
}

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Show per-node ENI and IP capacity: instance type limits vs current allocation",
	Args:  cobra.NoArgs,
	RunE:  runNode,
}

func init() {
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

	// Eligible node data collected for batch limit query and ENI queries.
	type eligibleNode struct {
		name         string
		instanceType string
		instanceID   string
		computeType  k8spkg.ComputeType
		note         string
	}
	var eligible []eligibleNode
	uniqueTypes := map[string]struct{}{}

	// 5. Classify each node.
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		ct := k8spkg.DetectNodeComputeType(node)

		switch ct {
		case k8spkg.ComputeTypeFargate:
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   "Fargate node — no EC2 ENIs",
			})
			continue

		case k8spkg.ComputeTypeAutoMode:
			// Fall through — proceed with note.
		}

		// 6. Extract instance type label and provider ID.
		instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]
		if !ok || instanceType == "" {
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   "missing instance-type label",
			})
			continue
		}

		instanceID, err := awspkg.ParseInstanceID(node.Spec.ProviderID)
		if err != nil {
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("cannot parse providerID: %s", node.Spec.ProviderID),
			})
			continue
		}

		note := ""
		if ct == k8spkg.ComputeTypeAutoMode {
			note = "EKS Auto Mode — ENI management is AWS-managed; limits may differ"
		}

		eligible = append(eligible, eligibleNode{
			name:         node.Name,
			instanceType: instanceType,
			instanceID:   instanceID,
			computeType:  ct,
			note:         note,
		})
		uniqueTypes[instanceType] = struct{}{}
	}

	// 7. Batch-query instance type limits.
	typeList := make([]string, 0, len(uniqueTypes))
	for t := range uniqueTypes {
		typeList = append(typeList, t)
	}

	limitsMap, err := awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)
	if err != nil {
		return fmt.Errorf("failed to get instance type limits: %w", err)
	}

	// 8-9. Per-node ENI query and utilization calculation.
	for _, en := range eligible {
		eniInfo, err := awspkg.ListNodeENIs(ctx, ec2Client, en.instanceID)
		if err != nil {
			if isVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] warning: could not list ENIs for node %s (%s): %v\n",
					en.name, en.instanceID, err)
			}
			report.Skipped = append(report.Skipped, SkippedNode{
				NodeName: en.name,
				Reason:   fmt.Sprintf("ENI query failed: %v", err),
			})
			continue
		}

		limits := limitsMap[en.instanceType]
		var maxENIs, maxIPsPerENI int32
		var maxTotalIPs int
		if limits != nil {
			maxENIs = limits.MaxENIs
			maxIPsPerENI = limits.MaxIPsPerENI
			maxTotalIPs = int(maxENIs) * int(maxIPsPerENI)
		}

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
			NodeName:     en.name,
			InstanceType: en.instanceType,
			ComputeType:  string(en.computeType),
			MaxENIs:      maxENIs,
			MaxIPsPerENI: maxIPsPerENI,
			CurrentENIs:  currentENIs,
			CurrentIPs:   currentIPs,
			MaxTotalIPs:  maxTotalIPs,
			Utilization:  strconv.Itoa(utilPct),
			Status:       status,
			Note:         en.note,
		})
	}

	// 10. Populate summary.
	report.Summary.CheckedNodes = len(report.Nodes)
	report.Summary.SkippedNodes = len(report.Skipped)

	// 11. Output.
	outFmt := getOutputFormat()
	if outFmt == "json" {
		jp, err := output.NewJSONPrinter(os.Stdout)
		if err != nil {
			return err
		}
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

	if len(report.Skipped) > 0 {
		skippedPrinter := output.NewTablePrinter(os.Stdout)
		skippedPrinter.PrintHeader("NODE", "REASON")
		for _, s := range report.Skipped {
			skippedPrinter.PrintRow(s.NodeName, s.Reason)
		}
		if err := skippedPrinter.Flush(); err != nil {
			return err
		}
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

// outgoingString formats the trailing summary line.
func outgoingString(checked, skipped, atRisk int) string {
	return fmt.Sprintf("\n%d nodes checked, %d skipped, %d at risk\n", checked, skipped, atRisk)
}
