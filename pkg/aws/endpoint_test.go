package aws

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func TestClassifyIP_Private(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"10.0.1.5", "private"},
		{"172.16.0.1", "private"},
		{"172.31.255.255", "private"},
		{"192.168.1.1", "private"},
		{"54.239.28.85", "public"},
		{"3.5.140.2", "public"},
		// Loopback
		{"127.0.0.1", "private"},
		{"127.255.255.255", "private"},
		// Link-local / AWS metadata
		{"169.254.169.254", "private"},
		{"169.254.0.1", "private"},
		// IPv6 loopback
		{"::1", "private"},
		// IPv6 ULA (fc00::/7)
		{"fd00::1", "private"},
		{"fc00::1", "private"},
		// IPv6 link-local
		{"fe80::1", "private"},
		// IPv6 public (documentation range — not private)
		{"2001:db8::1", "public"},
		// IPv4 public (regression)
		{"8.8.8.8", "public"},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := ClassifyIP(net.ParseIP(tt.ip))
			if got != tt.want {
				t.Errorf("ClassifyIP(%s) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestEnrichWithVpcEndpoints_Error(t *testing.T) {
	sentinel := errors.New("api unavailable")
	mock := &mockEC2API{
		describeVpcEndpoints: func(_ context.Context, _ *ec2.DescribeVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
			return nil, sentinel
		},
	}

	input := []EndpointCheckResult{
		{ServiceKey: "sts", DNSResult: "private"},
	}

	got, err := EnrichWithVpcEndpoints(context.Background(), mock, "us-east-1", input)

	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected error to wrap sentinel; got: %v", err)
	}
	if len(got) != len(input) {
		t.Errorf("expected results slice len %d, got %d", len(input), len(got))
	}
	if got[0].ServiceKey != input[0].ServiceKey {
		t.Errorf("results slice mutated: got service key %q, want %q", got[0].ServiceKey, input[0].ServiceKey)
	}
}

func TestBuildServiceEndpoints(t *testing.T) {
	endpoints := BuildServiceEndpoints("us-east-1")
	if len(endpoints) == 0 {
		t.Fatal("expected non-empty service endpoints")
	}

	found := map[string]bool{}
	for _, ep := range endpoints {
		found[ep.ServiceKey] = true
	}
	for _, key := range []string{"sts", "ec2", "ecr.api", "ecr.dkr", "s3", "logs"} {
		if !found[key] {
			t.Errorf("missing expected service key %q", key)
		}
	}
}
