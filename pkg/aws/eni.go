package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ENISummary holds a summary of a single ENI attached to a node.
type ENISummary struct {
	ENIID          string   `json:"eni_id"`
	DeviceIndex    int32    `json:"device_index"`
	Description    string   `json:"description"`
	PrivateIPCount int      `json:"private_ip_count"`
	SecurityGroups []string `json:"security_groups"`
}

// NodeENIInfo holds all ENI information for a node instance.
type NodeENIInfo struct {
	InstanceID string       `json:"instance_id"`
	ENIs       []ENISummary `json:"enis"`
	TotalIPs   int          `json:"total_ips"`
}

// InstanceLimits holds the ENI and IP limits for an EC2 instance type.
type InstanceLimits struct {
	InstanceType string `json:"instance_type"`
	MaxENIs      int32  `json:"max_enis"`
	MaxIPsPerENI int32  `json:"max_ips_per_eni"`
}

// ListNodeENIs returns all ENIs attached to the given EC2 instance.
func ListNodeENIs(ctx context.Context, api EC2API, instanceID string) (*NodeENIInfo, error) {
	out, err := api.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.instance-id"), Values: []string{instanceID}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeNetworkInterfaces (instance %s): %w", instanceID, err)
	}

	info := &NodeENIInfo{
		InstanceID: instanceID,
		ENIs:       make([]ENISummary, 0, len(out.NetworkInterfaces)),
	}

	for _, eni := range out.NetworkInterfaces {
		deviceIndex := int32(0)
		if eni.Attachment != nil && eni.Attachment.DeviceIndex != nil {
			deviceIndex = *eni.Attachment.DeviceIndex
		}

		groups := make([]string, 0, len(eni.Groups))
		for _, g := range eni.Groups {
			groups = append(groups, aws.ToString(g.GroupId))
		}

		ipCount := len(eni.PrivateIpAddresses)
		info.TotalIPs += ipCount

		info.ENIs = append(info.ENIs, ENISummary{
			ENIID:          aws.ToString(eni.NetworkInterfaceId),
			DeviceIndex:    deviceIndex,
			Description:    aws.ToString(eni.Description),
			PrivateIPCount: ipCount,
			SecurityGroups: groups,
		})
	}

	return info, nil
}

// GetInstanceTypeLimits returns the ENI and IP-per-ENI limits for the given
// EC2 instance types.
func GetInstanceTypeLimits(ctx context.Context, api EC2API, instanceTypes []string) (map[string]*InstanceLimits, error) {
	result := make(map[string]*InstanceLimits)
	if len(instanceTypes) == 0 {
		return result, nil
	}

	itypes := make([]ec2types.InstanceType, 0, len(instanceTypes))
	for _, it := range instanceTypes {
		itypes = append(itypes, ec2types.InstanceType(it))
	}

	out, err := api.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: itypes,
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeInstanceTypes: %w", err)
	}

	for _, info := range out.InstanceTypes {
		if info.NetworkInfo == nil {
			continue
		}
		limits := &InstanceLimits{
			InstanceType: string(info.InstanceType),
		}
		if info.NetworkInfo.MaximumNetworkInterfaces != nil {
			limits.MaxENIs = *info.NetworkInfo.MaximumNetworkInterfaces
		}
		if info.NetworkInfo.Ipv4AddressesPerInterface != nil {
			limits.MaxIPsPerENI = *info.NetworkInfo.Ipv4AddressesPerInterface
		}
		result[string(info.InstanceType)] = limits
	}

	return result, nil
}

// NodeInput describes a Kubernetes node to evaluate for ENI/IP utilization.
type NodeInput struct {
	Name         string
	InstanceID   string
	InstanceType string
}

// ENISkippedNode records a node that could not be evaluated due to an API error.
type ENISkippedNode struct {
	NodeName string
	Reason   string
}

// NodeUtilization holds the computed ENI and IP utilization for a single node.
type NodeUtilization struct {
	NodeName       string `json:"node_name"`
	InstanceType   string `json:"instance_type"`
	MaxENIs        int32  `json:"max_enis"`
	MaxIPsPerENI   int32  `json:"max_ips_per_eni"`
	CurrentENIs    int    `json:"current_enis"`
	CurrentIPs     int    `json:"current_ips"`
	MaxTotalIPs    int    `json:"max_total_ips"`
	UtilizationPct int    `json:"utilization_pct"`
	Status         string `json:"status"`
}

// ComputeNodeUtilization calculates ENI and IP utilization for a set of nodes.
// It batch-fetches instance type limits, then queries per-node ENI data.
// Nodes whose ENI queries fail are collected in the returned skipped slice rather
// than returning a terminal error. GetInstanceTypeLimits failure is terminal.
// When prefixDelegation is true the effective IP capacity is multiplied by 16.
// Status thresholds: >=85 → "EXHAUSTED", >=70 → "WARNING", else "OK".
func ComputeNodeUtilization(ctx context.Context, api EC2API, nodes []NodeInput, prefixDelegation bool) ([]NodeUtilization, []ENISkippedNode, error) {
	if len(nodes) == 0 {
		return []NodeUtilization{}, []ENISkippedNode{}, nil
	}

	// Collect unique instance types for a single batch limits call.
	seen := make(map[string]struct{})
	itypes := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if _, ok := seen[n.InstanceType]; !ok {
			seen[n.InstanceType] = struct{}{}
			itypes = append(itypes, n.InstanceType)
		}
	}

	limits, err := GetInstanceTypeLimits(ctx, api, itypes)
	if err != nil {
		return nil, nil, fmt.Errorf("GetInstanceTypeLimits: %w", err)
	}

	utils := make([]NodeUtilization, 0, len(nodes))
	skipped := make([]ENISkippedNode, 0)

	for _, node := range nodes {
		eniInfo, err := ListNodeENIs(ctx, api, node.InstanceID)
		if err != nil {
			skipped = append(skipped, ENISkippedNode{
				NodeName: node.Name,
				Reason:   err.Error(),
			})
			continue
		}

		var maxENIs, maxIPsPerENI int32
		if lim, ok := limits[node.InstanceType]; ok {
			maxENIs = lim.MaxENIs
			maxIPsPerENI = lim.MaxIPsPerENI
		}

		maxTotalIPs := int(maxENIs) * int(maxIPsPerENI)
		if prefixDelegation {
			maxTotalIPs *= 16
		}

		utilizationPct := 0
		if maxTotalIPs > 0 {
			utilizationPct = (eniInfo.TotalIPs * 100) / maxTotalIPs
		}

		status := "OK"
		switch {
		case utilizationPct >= 85:
			status = "EXHAUSTED"
		case utilizationPct >= 70:
			status = "WARNING"
		}

		utils = append(utils, NodeUtilization{
			NodeName:       node.Name,
			InstanceType:   node.InstanceType,
			MaxENIs:        maxENIs,
			MaxIPsPerENI:   maxIPsPerENI,
			CurrentENIs:    len(eniInfo.ENIs),
			CurrentIPs:     eniInfo.TotalIPs,
			MaxTotalIPs:    maxTotalIPs,
			UtilizationPct: utilizationPct,
			Status:         status,
		})
	}

	return utils, skipped, nil
}
