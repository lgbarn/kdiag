package eks

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
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
		return ""
	}
	return region
}

// newEC2Client resolves the AWS region and constructs a new EC2 API client.
func newEC2Client(ctx context.Context, host string) (awspkg.EC2API, error) {
	region := resolveRegion(host)
	return awspkg.NewEC2Client(ctx, region, awsProfile)
}

// Ensure unexported helpers are referenced so the compiler does not complain.
var _ = getOutputFormat
var _ = getTimeout
var _ = isVerbose
var _ = newEC2Client
