package eks

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// CNIReport is the top-level JSON report for kdiag eks cni.
type CNIReport struct {
	DaemonSet   DaemonSetStatus `json:"daemonset"`
	Config      CNIConfig       `json:"config"`
	Nodes       []NodeCapacity  `json:"nodes"`
	Skipped     []SkippedNode   `json:"skipped_nodes"`
	IPExhausted []string        `json:"ip_exhausted_nodes"`
}

// DaemonSetStatus holds the aws-node DaemonSet status fields.
type DaemonSetStatus struct {
	Desired int32 `json:"desired"`
	Ready   int32 `json:"ready"`
	Updated int32 `json:"updated"`
	Healthy bool  `json:"healthy"`
}

// CNIConfig holds the relevant env-var configuration extracted from the
// aws-node DaemonSet container.
type CNIConfig struct {
	PrefixDelegation bool   `json:"prefix_delegation"`
	PodENI           bool   `json:"pod_eni"`
	WarmIPTarget     string `json:"warm_ip_target"`
	WarmENITarget    string `json:"warm_eni_target"`
	WarmPrefixTarget string `json:"warm_prefix_target"`
	MinimumIPTarget  string `json:"minimum_ip_target"`
}

// NodeCapacity holds per-node ENI/IP capacity data.
type NodeCapacity struct {
	NodeName     string `json:"node_name"`
	InstanceType string `json:"instance_type"`
	MaxENIs      int32  `json:"max_enis"`
	MaxIPsPerENI int32  `json:"max_ips_per_eni"`
	MaxTotalIPs  int    `json:"max_total_ips"`
	CurrentENIs  int    `json:"current_enis"`
	CurrentIPs   int    `json:"current_ips"`
	Utilization  string `json:"utilization_pct"`
	Exhausted    bool   `json:"exhausted"`
}

var cniCmd = &cobra.Command{
	Use:   "cni",
	Short: "Show VPC CNI configuration and per-node IP capacity",
	Long: `Inspect the aws-node DaemonSet configuration and report per-node ENI/IP
capacity against current usage. Nodes above 85% IP utilization are flagged
as exhausted. Prefix delegation mode multiplies capacity by 16.`,
	Args: cobra.NoArgs,
	RunE: runCNI,
}

func init() {
	EksCmd.AddCommand(cniCmd)
}

func runCNI(cmd *cobra.Command, args []string) error {
	// 1. Build Kubernetes client and verify EKS.
	k8sClient, err := k8s.NewClient(configFlags)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	if err := requireEKS(k8sClient.Config.Host); err != nil {
		return err
	}

	// 2. Context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), getTimeout())
	defer cancel()

	// 3. Get aws-node DaemonSet from kube-system.
	ds, err := k8sClient.Clientset.AppsV1().DaemonSets("kube-system").Get(ctx, "aws-node", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get aws-node DaemonSet: %w", err)
	}

	dsStatus := DaemonSetStatus{
		Desired: ds.Status.DesiredNumberScheduled,
		Ready:   ds.Status.NumberReady,
		Updated: ds.Status.UpdatedNumberScheduled,
		Healthy: ds.Status.NumberReady == ds.Status.DesiredNumberScheduled,
	}

	// Extract env vars from the aws-node container.
	cniConfig := CNIConfig{}
	for _, container := range ds.Spec.Template.Spec.Containers {
		if container.Name != "aws-node" {
			continue
		}
		for _, env := range container.Env {
			switch env.Name {
			case "ENABLE_PREFIX_DELEGATION":
				cniConfig.PrefixDelegation = env.Value == "true"
			case "ENABLE_POD_ENI":
				cniConfig.PodENI = env.Value == "true"
			case "WARM_IP_TARGET":
				cniConfig.WarmIPTarget = env.Value
			case "WARM_ENI_TARGET":
				cniConfig.WarmENITarget = env.Value
			case "WARM_PREFIX_TARGET":
				cniConfig.WarmPrefixTarget = env.Value
			case "MINIMUM_IP_TARGET":
				cniConfig.MinimumIPTarget = env.Value
			}
		}
	}

	// 4. Build EC2 client.
	ec2Client, err := newEC2Client(ctx, k8sClient.Config.Host)
	if err != nil {
		return fmt.Errorf("failed to create EC2 client: %w", err)
	}

	// 5. List nodes.
	nodeList, err := k8sClient.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// Determine if prefix delegation is enabled.
	prefixDelegation := cniConfig.PrefixDelegation

	eligible, skipped := ClassifyNodes(nodeList.Items)
	uniqueTypes := map[string]struct{}{}
	for _, en := range eligible {
		uniqueTypes[en.InstanceType] = struct{}{}
	}

	// 6. Batch query instance type limits.
	typeList := uniqueKeys(uniqueTypes)

	limitsMap, err := awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)
	if err != nil {
		return fmt.Errorf("failed to get instance type limits: %w", err)
	}

	// 7. Per-node ENI query and utilization calculation.
	var nodes []NodeCapacity
	var ipExhausted []string

	for _, en := range eligible {
		eniInfo, err := awspkg.ListNodeENIs(ctx, ec2Client, en.InstanceID)
		if err != nil {
			if isVerbose() {
				fmt.Fprintf(os.Stderr, "[kdiag] warning: could not list ENIs for node %s (%s): %v\n",
					en.Name, en.InstanceID, err)
			}
			skipped = append(skipped, SkippedNode{
				NodeName: en.Name,
				Reason:   fmt.Sprintf("ENI query failed: %v", err),
			})
			continue
		}

		limits := limitsMap[en.InstanceType]
		var maxENIs, maxIPsPerENI int32
		var maxTotalIPs int
		if limits != nil {
			maxENIs = limits.MaxENIs
			maxIPsPerENI = limits.MaxIPsPerENI
			// 8. If prefix delegation, multiply capacity by 16.
			if prefixDelegation {
				maxTotalIPs = int(maxENIs) * int(maxIPsPerENI) * 16
			} else {
				maxTotalIPs = int(maxENIs) * int(maxIPsPerENI)
			}
		}

		currentENIs := len(eniInfo.ENIs)
		currentIPs := eniInfo.TotalIPs

		utilPct := 0
		if maxTotalIPs > 0 {
			utilPct = (currentIPs * 100) / maxTotalIPs
		}

		// Exhausted if >= 85%.
		exhausted := utilPct >= 85
		if exhausted {
			ipExhausted = append(ipExhausted, en.Name)
		}

		nodes = append(nodes, NodeCapacity{
			NodeName:     en.Name,
			InstanceType: en.InstanceType,
			MaxENIs:      maxENIs,
			MaxIPsPerENI: maxIPsPerENI,
			MaxTotalIPs:  maxTotalIPs,
			CurrentENIs:  currentENIs,
			CurrentIPs:   currentIPs,
			Utilization:  strconv.Itoa(utilPct),
			Exhausted:    exhausted,
		})
	}

	// 9. Build CNIReport.
	report := CNIReport{
		DaemonSet:   dsStatus,
		Config:      cniConfig,
		Nodes:       nodes,
		Skipped:     skipped,
		IPExhausted: ipExhausted,
	}

	// 10. Output.
	printer, err := output.NewPrinter(getOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}
	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(report)
	}

	return printCNITable(report, prefixDelegation)
}

// printCNITable renders the CNIReport as a human-readable table with 3 sections:
// DaemonSet status, CNI configuration, and per-node capacity.
func printCNITable(r CNIReport, prefixDelegation bool) error {
	// Section 1: DaemonSet Status.
	fmt.Fprintln(os.Stdout, "=== aws-node DaemonSet Status ===")
	dsPrinter := output.NewTablePrinter(os.Stdout)
	dsPrinter.PrintHeader("DESIRED", "READY", "UPDATED")
	dsPrinter.PrintRow(
		strconv.Itoa(int(r.DaemonSet.Desired)),
		strconv.Itoa(int(r.DaemonSet.Ready)),
		strconv.Itoa(int(r.DaemonSet.Updated)),
	)
	if err := dsPrinter.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout)

	// Section 2: CNI Configuration.
	fmt.Fprintln(os.Stdout, "=== VPC CNI Configuration ===")
	cfgPrinter := output.NewTablePrinter(os.Stdout)
	cfgPrinter.PrintHeader("SETTING", "VALUE")
	cfgPrinter.PrintRow("ENABLE_PREFIX_DELEGATION", strconv.FormatBool(r.Config.PrefixDelegation))
	cfgPrinter.PrintRow("ENABLE_POD_ENI", strconv.FormatBool(r.Config.PodENI))
	cfgPrinter.PrintRow("WARM_IP_TARGET", r.Config.WarmIPTarget)
	cfgPrinter.PrintRow("WARM_ENI_TARGET", r.Config.WarmENITarget)
	cfgPrinter.PrintRow("WARM_PREFIX_TARGET", r.Config.WarmPrefixTarget)
	cfgPrinter.PrintRow("MINIMUM_IP_TARGET", r.Config.MinimumIPTarget)
	if err := cfgPrinter.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout)

	// Section 3: Per-node Capacity.
	prefixNote := ""
	if prefixDelegation {
		prefixNote = " (prefix delegation: x16)"
	}
	fmt.Fprintf(os.Stdout, "=== Node IP Capacity%s ===\n", prefixNote)
	nodePrinter := output.NewTablePrinter(os.Stdout)
	nodePrinter.PrintHeader("NODE", "INSTANCE_TYPE", "MAX_ENIS", "MAX_IPS/ENI", "MAX_IPS", "CURRENT_IPS", "UTIL%", "STATUS")
	for _, n := range r.Nodes {
		status := "OK"
		if n.Exhausted {
			status = "EXHAUSTED"
		}
		nodePrinter.PrintRow(
			n.NodeName,
			n.InstanceType,
			strconv.Itoa(int(n.MaxENIs)),
			strconv.Itoa(int(n.MaxIPsPerENI)),
			strconv.Itoa(n.MaxTotalIPs),
			strconv.Itoa(n.CurrentIPs),
			n.Utilization+"%",
			status,
		)
	}
	if err := nodePrinter.Flush(); err != nil {
		return err
	}

	if err := printSkippedNodes(r.Skipped); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "\n%d nodes checked, %d exhausted, %d skipped\n",
		len(r.Nodes), len(r.IPExhausted), len(r.Skipped))

	return nil
}
