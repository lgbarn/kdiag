package aws

import (
	"context"
	"fmt"
	"net"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ServiceEndpoint represents an AWS service to check for VPC endpoints.
type ServiceEndpoint struct {
	ServiceKey string // short name: "sts", "ec2", etc.
	Hostname   string // DNS hostname to resolve
	AWSService string // full service name for DescribeVpcEndpoints filter
}

// EndpointCheckResult holds the result of checking a single service.
type EndpointCheckResult struct {
	ServiceKey   string   `json:"service_key"`
	DNSResult    string   `json:"dns_result"`
	ResolvedIPs  []string `json:"resolved_ips,omitempty"`
	EndpointType string   `json:"endpoint_type,omitempty"`
	EndpointID   string   `json:"endpoint_id,omitempty"`
	State        string   `json:"state,omitempty"`
}

// BuildServiceEndpoints returns the AWS services to check for VPC endpoints.
func BuildServiceEndpoints(region string) []ServiceEndpoint {
	return []ServiceEndpoint{
		{ServiceKey: "sts", Hostname: fmt.Sprintf("sts.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.sts", region)},
		{ServiceKey: "ec2", Hostname: fmt.Sprintf("ec2.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ec2", region)},
		{ServiceKey: "ecr.api", Hostname: fmt.Sprintf("api.ecr.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ecr.api", region)},
		{ServiceKey: "ecr.dkr", Hostname: fmt.Sprintf("dkr.ecr.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ecr.dkr", region)},
		{ServiceKey: "s3", Hostname: fmt.Sprintf("s3.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.s3", region)},
		{ServiceKey: "logs", Hostname: fmt.Sprintf("logs.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.logs", region)},
	}
}

// ClassifyIP returns "private" for RFC 1918 addresses, "public" otherwise.
func ClassifyIP(ip net.IP) string {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return "private"
		}
	}
	return "public"
}

// DNSResolver is a function type for resolving hostnames. Allows test injection.
type DNSResolver func(host string) ([]net.IP, error)

// DefaultDNSResolver uses net.LookupIP.
func DefaultDNSResolver(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// CheckEndpointDNS resolves the hostname and classifies the result.
func CheckEndpointDNS(ep ServiceEndpoint, resolver DNSResolver) EndpointCheckResult {
	result := EndpointCheckResult{ServiceKey: ep.ServiceKey}
	ips, err := resolver(ep.Hostname)
	if err != nil || len(ips) == 0 {
		result.DNSResult = "unresolved"
		return result
	}
	result.DNSResult = ClassifyIP(ips[0])
	for _, ip := range ips {
		result.ResolvedIPs = append(result.ResolvedIPs, ip.String())
	}
	return result
}

// EnrichWithVpcEndpoints calls DescribeVpcEndpoints and enriches results.
func EnrichWithVpcEndpoints(ctx context.Context, api EC2API, region string, results []EndpointCheckResult) []EndpointCheckResult {
	endpoints := BuildServiceEndpoints(region)
	svcToIdx := map[string]int{}
	for i, r := range results {
		for _, ep := range endpoints {
			if ep.ServiceKey == r.ServiceKey {
				svcToIdx[ep.AWSService] = i
				break
			}
		}
	}
	svcNames := make([]string, 0, len(svcToIdx))
	for sn := range svcToIdx {
		svcNames = append(svcNames, sn)
	}
	if len(svcNames) == 0 {
		return results
	}
	out, err := api.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("service-name"), Values: svcNames},
		},
	})
	if err != nil {
		return results
	}
	for _, vpce := range out.VpcEndpoints {
		svcName := aws.ToString(vpce.ServiceName)
		idx, ok := svcToIdx[svcName]
		if !ok {
			continue
		}
		results[idx].EndpointID = aws.ToString(vpce.VpcEndpointId)
		results[idx].EndpointType = string(vpce.VpcEndpointType)
		results[idx].State = string(vpce.State)
	}
	return results
}
