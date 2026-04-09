package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/lgbarn/kdiag/cmd/eks"
)

// imageRefRe validates OCI image references: registry/repo:tag or registry/repo@sha256:digest.
// The domain portion (before the first /) allows uppercase (DNS is case-insensitive).
// Path components (after /) are lowercase per the OCI Distribution Spec, with separators
// matching the OCI grammar: single [._], double [__], or one-or-more [-].
// Tags (after :) allow mixed case.
var imageRefRe = regexp.MustCompile(`^[a-zA-Z0-9]+([._-][a-zA-Z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._]{1,2}|-+)[a-z0-9]+|/[a-z0-9]+)*(:[a-zA-Z0-9._-]+)?(@sha256:[a-f0-9]{64})?$`)

var (
	// ConfigFlags holds standard kubectl-style kubeconfig/context/namespace flags.
	ConfigFlags *genericclioptions.ConfigFlags

	// outputFormat is the --output / -o flag value (table, json).
	outputFormat string

	// debugImage is the --image flag: debug container image to use.
	debugImage string

	// imagePullSecret is the --image-pull-secret flag.
	imagePullSecret string

	// timeout is the --timeout flag duration for operations.
	timeout time.Duration

	// verbose enables debug logging when true.
	verbose bool

	// awsProfile is the --profile flag: AWS shared config profile to use.
	awsProfile string

	// awsRegion is the --region flag: AWS region override.
	awsRegion string
)

var rootCmd = &cobra.Command{
	Use:           "kdiag",
	Short:         "Kubernetes diagnostics CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	// Initialize standard kubectl-compatible config flags (kubeconfig, context, namespace, etc.)
	ConfigFlags = genericclioptions.NewConfigFlags(true)
	ConfigFlags.AddFlags(rootCmd.PersistentFlags())

	// Custom global flags for kdiag
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table or json")
	rootCmd.PersistentFlags().StringVar(&debugImage, "image", "nicolaka/netshoot:v0.13", "Debug container image")
	rootCmd.PersistentFlags().StringVar(&imagePullSecret, "image-pull-secret", "", "Image pull secret for private registry debug images")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "Timeout for operations (e.g. 30s, 2m)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug logging")
	rootCmd.PersistentFlags().StringVar(&awsProfile, "profile", "", "AWS shared config profile to use")
	rootCmd.PersistentFlags().StringVar(&awsRegion, "region", "", "AWS region override (auto-detected from EKS endpoint when omitted)")

	eks.Init(rootCmd, ConfigFlags, &outputFormat, &timeout, &verbose, &awsProfile, &awsRegion)
}

// Execute runs the root command. Returns any error.
func Execute() error {
	return rootCmd.Execute()
}

// GetOutputFormat returns the value of the --output flag.
func GetOutputFormat() string { return outputFormat }

// GetDebugImage returns the value of the --image flag.
func GetDebugImage() string { return debugImage }

// ValidateDebugImage checks the --image flag value is a valid OCI image reference.
func ValidateDebugImage() error {
	if debugImage == "" {
		return fmt.Errorf("--image must not be empty")
	}
	if !imageRefRe.MatchString(debugImage) {
		return fmt.Errorf("--image %q is not a valid container image reference", debugImage)
	}
	return nil
}

// GetImagePullSecret returns the value of the --image-pull-secret flag.
func GetImagePullSecret() string { return imagePullSecret }

// GetTimeout returns the value of the --timeout flag.
func GetTimeout() time.Duration { return timeout }

// IsVerbose returns true when --verbose/-v is set.
func IsVerbose() bool { return verbose }

// GetAWSProfile returns the value of the --profile flag.
func GetAWSProfile() string { return awsProfile }

// GetAWSRegion returns the value of the --region flag.
func GetAWSRegion() string { return awsRegion }

// StripPodPrefix removes a "pod/" prefix from an argument so that commands
// accepting bare pod names also work when called with "pod/my-pod" for
// consistency with the type/name format used by inspect.
func StripPodPrefix(arg string) string {
	lower := strings.ToLower(arg)
	if strings.HasPrefix(lower, "pod/") {
		return arg[4:]
	}
	return arg
}
