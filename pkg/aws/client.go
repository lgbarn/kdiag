package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// NewEC2Client loads AWS configuration from the environment or shared config
// files and returns an EC2API client.  region and profile are optional; empty
// strings are silently ignored.
//
// A credential pre-flight check is performed so callers receive a clear error
// message before any real API call is made.
func NewEC2Client(ctx context.Context, region, profile string) (EC2API, error) {
	opts := []func(*config.LoadOptions) error{}

	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Pre-flight credential check — fail fast with an actionable message.
	if _, err = cfg.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("AWS credentials not found: %w\n\nConfigure credentials with one of:\n  export AWS_ACCESS_KEY_ID=...\n  aws configure\n  Use IAM role / IRSA when running in-cluster", err)
	}

	return ec2.NewFromConfig(cfg), nil
}
