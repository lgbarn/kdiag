package eks

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	k8spkg "github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// EndpointReport is the structured result for --output json.
type EndpointReport struct {
	Region      string                       `json:"region"`
	EKSPrivate  string                       `json:"eks_api_access"`
	Services    []awspkg.EndpointCheckResult `json:"services"`
	APIEnriched bool                         `json:"api_enriched"`
}

var endpointCmd = &cobra.Command{
	Use:   "endpoint",
	Short: "Check VPC endpoints for AWS services (STS, EC2, ECR, S3, CloudWatch Logs, EKS API)",
	Args:  cobra.NoArgs,
	RunE:  runEndpoint,
}

func init() {
	EksCmd.AddCommand(endpointCmd)
}

func runEndpoint(cmd *cobra.Command, args []string) error {
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

	// 3. Resolve region and build service endpoints.
	region := resolveRegion(k8sClient.Config.Host)
	serviceEndpoints := awspkg.BuildServiceEndpoints(region)

	// 4. Phase 1: DNS-resolve each service endpoint.
	results := make([]awspkg.EndpointCheckResult, 0, len(serviceEndpoints))
	for _, ep := range serviceEndpoints {
		results = append(results, awspkg.CheckEndpointDNS(ep, awspkg.DefaultDNSResolver))
	}

	// 5. EKS API check: extract hostname, DNS-resolve, classify as private/public.
	eksHostname := extractHostname(k8sClient.Config.Host)
	eksAccess := "unresolved"
	if eksHostname != "" {
		fakeEP := awspkg.ServiceEndpoint{
			ServiceKey: "eks-api",
			Hostname:   eksHostname,
		}
		eksResult := awspkg.CheckEndpointDNS(fakeEP, awspkg.DefaultDNSResolver)
		eksAccess = eksResult.DNSResult
	}

	// 6. Phase 2: try EC2 client; if it succeeds enrich with VPC endpoint data.
	apiEnriched := false
	ec2Client, ec2Err := newEC2Client(ctx, k8sClient.Config.Host)
	if ec2Err == nil {
		var enrichErr error
		results, enrichErr = awspkg.EnrichWithVpcEndpoints(ctx, ec2Client, region, results)
		if enrichErr != nil {
			fmt.Fprintf(os.Stderr, "[kdiag] warning: VPC endpoint enrichment failed: %v\n", enrichErr)
		} else {
			apiEnriched = true
		}
	}

	// 7. Build report and output.
	report := EndpointReport{
		Region:      region,
		EKSPrivate:  eksAccess,
		Services:    results,
		APIEnriched: apiEnriched,
	}

	printer, err := output.NewPrinter(getOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}
	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(report)
	}
	return printEndpointTable(report)
}

// printEndpointTable renders the EndpointReport as a human-readable table.
func printEndpointTable(report EndpointReport) error {
	fmt.Fprintf(os.Stdout, "Region:         %s\n", report.Region)
	fmt.Fprintf(os.Stdout, "EKS API Access: %s\n", report.EKSPrivate)
	if !report.APIEnriched {
		fmt.Fprintln(os.Stdout, "Note: DNS results only — no EC2 API access")
	}
	fmt.Fprintln(os.Stdout)

	p := output.NewTablePrinter(os.Stdout)
	if report.APIEnriched {
		p.PrintHeader("SERVICE", "DNS_RESULT", "ENDPOINT_TYPE", "ENDPOINT_ID", "STATE")
		for _, svc := range report.Services {
			p.PrintRow(
				svc.ServiceKey,
				svc.DNSResult,
				svc.EndpointType,
				svc.EndpointID,
				svc.State,
			)
		}
	} else {
		p.PrintHeader("SERVICE", "DNS_RESULT")
		for _, svc := range report.Services {
			p.PrintRow(svc.ServiceKey, svc.DNSResult)
		}
	}
	return p.Flush()
}

// extractHostname parses host and returns just the hostname portion,
// stripping scheme, port, and path.
func extractHostname(host string) string {
	if host == "" {
		return ""
	}
	// If host has no scheme, url.Parse won't split host:port correctly.
	// Prepend a scheme if missing so net/url can parse it.
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil {
		return host
	}
	return u.Hostname()
}
