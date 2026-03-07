package aws

import (
	"net"
	"testing"
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
