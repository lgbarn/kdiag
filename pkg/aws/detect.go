package aws

import (
	"fmt"
	"net/url"
	"strings"
)

// IsEKSCluster returns true when host is a structurally valid EKS API server endpoint.
// It delegates to RegionFromHost so that substring-only matches (e.g. spoofed hostnames)
// are rejected; a host is accepted only when the full EKS segment structure is parseable.
func IsEKSCluster(host string) bool {
	_, err := RegionFromHost(host)
	return err == nil
}

// RegionFromHost extracts the AWS region from an EKS API server endpoint URL.
// The expected hostname format is: <id>.<az-code>.<region>.eks.amazonaws.com
//
// Returns an error when host is not a valid EKS endpoint.
func RegionFromHost(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("host is empty")
	}

	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("failed to parse host %q: %w", host, err)
	}

	hostname := u.Hostname() // strips scheme, port, and path

	// hostname: <id>.<az-code>.<region>.eks.amazonaws.com
	parts := strings.Split(hostname, ".")

	// Find the index of "eks" where the two following parts are "amazonaws" and "com".
	for i, part := range parts {
		if part == "eks" && i+2 < len(parts) && parts[i+1] == "amazonaws" && parts[i+2] == "com" {
			if i == 0 {
				return "", fmt.Errorf("unexpected hostname structure in %q: no segment before 'eks'", host)
			}
			return parts[i-1], nil
		}
	}

	return "", fmt.Errorf("host %q is not an EKS endpoint", host)
}

// ParseInstanceID extracts the EC2 instance ID from a node's provider ID.
// The expected format is: aws:///<availability-zone>/<instance-id>
//
// Returns an error when providerID is empty or not an AWS provider ID.
func ParseInstanceID(providerID string) (string, error) {
	if providerID == "" {
		return "", fmt.Errorf("providerID is empty")
	}
	if !strings.HasPrefix(providerID, "aws:///") {
		return "", fmt.Errorf("providerID %q is not an AWS provider ID", providerID)
	}

	parts := strings.Split(providerID, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("providerID %q has unexpected format", providerID)
	}

	instanceID := parts[len(parts)-1]
	if instanceID == "" {
		return "", fmt.Errorf("providerID %q has empty instance ID segment", providerID)
	}

	return instanceID, nil
}
