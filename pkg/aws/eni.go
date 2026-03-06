package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ENIDetail holds per-ENI information for a node.
type ENIDetail struct {
	ENIID          string   `json:"eni_id"`
	DeviceIndex    int32    `json:"device_index"`
	Description    string   `json:"description"`
	PrivateIPCount int      `json:"private_ip_count"`
	SecurityGroups []string `json:"security_groups"`
}

// NodeENIInfo holds all ENI information for a node instance.
type NodeENIInfo struct {
	InstanceID string      `json:"instance_id"`
	ENIs       []ENIDetail `json:"enis"`
	TotalIPs   int         `json:"total_ips"`
}

// InstanceTypeLimits holds the ENI and IP limits for an EC2 instance type.
type InstanceTypeLimits struct {
	MaxENIs      int32 `json:"max_enis"`
	MaxIPsPerENI int32 `json:"max_ips_per_eni"`
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
		ENIs:       make([]ENIDetail, 0, len(out.NetworkInterfaces)),
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

		info.ENIs = append(info.ENIs, ENIDetail{
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
func GetInstanceTypeLimits(ctx context.Context, api EC2API, instanceTypes []string) (map[string]*InstanceTypeLimits, error) {
	result := make(map[string]*InstanceTypeLimits)
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
		limits := &InstanceTypeLimits{}
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
