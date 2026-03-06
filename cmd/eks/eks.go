package eks

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	k8spkg "github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

var (
	configFlags  *genericclioptions.ConfigFlags
	outputFormat *string
	timeout      *time.Duration
	verbose      *bool
	awsProfile   string
	awsRegion    string
)

// EksCmd is the parent cobra command for EKS-specific diagnostics.
var EksCmd = &cobra.Command{
	Use:   "eks",
	Short: "EKS-specific diagnostics: VPC CNI, security groups, and node ENI capacity",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// Init stores the shared flag pointers coming from the root command,
// registers EKS-specific persistent flags, and attaches EksCmd to root.
func Init(
	root *cobra.Command,
	flags *genericclioptions.ConfigFlags,
	outFmt *string,
	tout *time.Duration,
	verb *bool,
) {
	configFlags = flags
	outputFormat = outFmt
	timeout = tout
	verbose = verb

	EksCmd.PersistentFlags().StringVar(&awsProfile, "aws-profile", "", "AWS shared config profile to use")
	EksCmd.PersistentFlags().StringVar(&awsRegion, "aws-region", "", "AWS region override (auto-detected from EKS endpoint when omitted)")

	root.AddCommand(EksCmd)
}

// getOutputFormat returns the current output format flag value.
func getOutputFormat() string {
	if outputFormat == nil {
		return "table"
	}
	return *outputFormat
}

// getTimeout returns the current timeout flag value.
func getTimeout() time.Duration {
	if timeout == nil {
		return 30 * time.Second
	}
	return *timeout
}

// isVerbose returns true when the verbose flag is set.
func isVerbose() bool {
	if verbose == nil {
		return false
	}
	return *verbose
}

// requireEKS returns an error when host is not an EKS cluster endpoint.
func requireEKS(host string) error {
	if !awspkg.IsEKSCluster(host) {
		return fmt.Errorf("current cluster (%q) does not appear to be an EKS cluster; EKS commands require an EKS API server endpoint (*.eks.amazonaws.com)", host)
	}
	return nil
}

// resolveRegion returns the AWS region to use: the explicit --aws-region flag
// value takes precedence, otherwise the region is parsed from the EKS host.
func resolveRegion(host string) string {
	if awsRegion != "" {
		return awsRegion
	}
	region, err := awspkg.RegionFromHost(host)
	if err != nil {
		if isVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] could not detect region from host %q: %v; falling back to AWS config\n", host, err)
		}
		return ""
	}
	return region
}

// newEC2Client resolves the AWS region and constructs a new EC2 API client.
func newEC2Client(ctx context.Context, host string) (awspkg.EC2API, error) {
	region := resolveRegion(host)
	return awspkg.NewEC2Client(ctx, region, awsProfile)
}

// SkippedNode records a node that was excluded from a report with a reason.
type SkippedNode struct {
	NodeName string `json:"node_name"`
	Reason   string `json:"reason"`
}

// EligibleNode is an EC2-backed node that passed classification for ENI/IP checks.
type EligibleNode struct {
	Name         string
	InstanceType string
	InstanceID   string
	ComputeType  k8spkg.ComputeType // empty for standard managed nodes
	Note         string             // non-empty for Auto Mode nodes
}

// ClassifyNodes partitions nodes into eligible EC2-backed nodes and skipped nodes.
// Fargate nodes are always skipped. Nodes missing an instance-type label or a
// parseable providerID are also skipped. Auto Mode nodes are included as eligible
// with a descriptive Note.
func ClassifyNodes(nodes []corev1.Node) (eligible []EligibleNode, skipped []SkippedNode) {
	for i := range nodes {
		node := &nodes[i]
		ct := k8spkg.DetectNodeComputeType(node)

		if ct == k8spkg.ComputeTypeFargate {
			skipped = append(skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   "Fargate node — no EC2 ENIs",
			})
			continue
		}

		instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]
		if !ok || instanceType == "" {
			skipped = append(skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   "missing instance-type label",
			})
			continue
		}

		instanceID, err := awspkg.ParseInstanceID(node.Spec.ProviderID)
		if err != nil {
			skipped = append(skipped, SkippedNode{
				NodeName: node.Name,
				Reason:   fmt.Sprintf("cannot parse providerID: %s", node.Spec.ProviderID),
			})
			continue
		}

		note := ""
		if ct == k8spkg.ComputeTypeAutoMode {
			note = "EKS Auto Mode — ENI management is AWS-managed; limits may differ"
		}

		eligible = append(eligible, EligibleNode{
			Name:         node.Name,
			InstanceType: instanceType,
			InstanceID:   instanceID,
			ComputeType:  ct,
			Note:         note,
		})
	}
	return
}

// uniqueKeys returns a slice of all keys from a map[string]struct{}.
func uniqueKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// printSkippedNodes renders a skipped-nodes table to stdout.
func printSkippedNodes(skipped []SkippedNode) error {
	if len(skipped) == 0 {
		return nil
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "=== Skipped Nodes ===")
	p := output.NewTablePrinter(os.Stdout)
	p.PrintHeader("NODE", "REASON")
	for _, s := range skipped {
		p.PrintRow(s.NodeName, s.Reason)
	}
	return p.Flush()
}

