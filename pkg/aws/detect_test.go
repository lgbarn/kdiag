package aws

import (
	"testing"
)

func TestIsEKSCluster(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"https://ABCDEF.gr7.us-east-1.eks.amazonaws.com", true},
		{"https://kubernetes.default.svc", false},
		{"https://my-cluster.example.com", false},
		{"", false},
	}
	for _, tc := range tests {
		got := IsEKSCluster(tc.host)
		if got != tc.want {
			t.Errorf("IsEKSCluster(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestRegionFromHost(t *testing.T) {
	tests := []struct {
		host    string
		want    string
		wantErr bool
	}{
		{"https://ABCDEF.gr7.us-east-1.eks.amazonaws.com", "us-east-1", false},
		{"https://XYZ.yl4.eu-west-1.eks.amazonaws.com", "eu-west-1", false},
		{"https://ABC.zz9.ap-southeast-1.eks.amazonaws.com:443", "ap-southeast-1", false},
		{"https://kubernetes.default.svc", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		got, err := RegionFromHost(tc.host)
		if tc.wantErr {
			if err == nil {
				t.Errorf("RegionFromHost(%q): expected error, got %q", tc.host, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("RegionFromHost(%q): unexpected error: %v", tc.host, err)
			continue
		}
		if got != tc.want {
			t.Errorf("RegionFromHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestParseInstanceID(t *testing.T) {
	tests := []struct {
		providerID string
		want       string
		wantErr    bool
	}{
		{"aws:///us-east-1a/i-0abc123def456789", "i-0abc123def456789", false},
		{"", "", true},
		{"gce:///zone/instance", "", true},
	}
	for _, tc := range tests {
		got, err := ParseInstanceID(tc.providerID)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseInstanceID(%q): expected error, got %q", tc.providerID, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseInstanceID(%q): unexpected error: %v", tc.providerID, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseInstanceID(%q) = %q, want %q", tc.providerID, got, tc.want)
		}
	}
}
