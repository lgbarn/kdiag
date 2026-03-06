package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// PodENIAnnotationKey is the annotation key used by the VPC CNI "security groups
// for pods" feature to store branch ENI details on a pod.
const PodENIAnnotationKey = "vpc.amazonaws.com/pod-eni"

// SecurityGroupDetail holds the full description of an EC2 security group.
type SecurityGroupDetail struct {
	GroupID      string   `json:"group_id"`
	GroupName    string   `json:"group_name"`
	Description  string   `json:"description"`
	IngressRules []SGRule `json:"ingress_rules"`
	EgressRules  []SGRule `json:"egress_rules"`
}

// SGRule represents a single inbound or outbound rule.
type SGRule struct {
	Protocol     string   `json:"protocol"`
	FromPort     int32    `json:"from_port"`
	ToPort       int32    `json:"to_port"`
	CIDRs        []string `json:"cidrs"`
	SourceGroups []string `json:"source_groups"`
	Description  string   `json:"description"`
}

// PodENIAnnotation holds the branch-ENI details stored in the
// vpc.amazonaws.com/pod-eni annotation used by the VPC CNI "security groups
// for pods" feature.
type PodENIAnnotation struct {
	ENIID      string `json:"eniId"`
	PrivateIP  string `json:"privateIp"`
	VlanID     int    `json:"vlanId"`
	SubnetCIDR string `json:"subnetCidr"`
}

// GetSecurityGroupDetails fetches full security-group details for the given
// group IDs via DescribeSecurityGroups and maps the AWS types to the kdiag
// domain types.
func GetSecurityGroupDetails(ctx context.Context, api EC2API, groupIDs []string) ([]SecurityGroupDetail, error) {
	out, err := api.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: groupIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeSecurityGroups: %w", err)
	}

	details := make([]SecurityGroupDetail, 0, len(out.SecurityGroups))
	for _, sg := range out.SecurityGroups {
		details = append(details, SecurityGroupDetail{
			GroupID:      aws.ToString(sg.GroupId),
			GroupName:    aws.ToString(sg.GroupName),
			Description:  aws.ToString(sg.Description),
			IngressRules: mapPermissions(sg.IpPermissions),
			EgressRules:  mapPermissions(sg.IpPermissionsEgress),
		})
	}
	return details, nil
}

// mapPermissions converts a slice of EC2 IpPermission values to SGRule values.
func mapPermissions(perms []ec2types.IpPermission) []SGRule {
	rules := make([]SGRule, 0, len(perms))
	for _, p := range perms {
		proto := aws.ToString(p.IpProtocol)
		if proto == "-1" {
			proto = "all"
		}

		cidrs := make([]string, 0, len(p.IpRanges))
		for _, r := range p.IpRanges {
			cidrs = append(cidrs, aws.ToString(r.CidrIp))
		}

		sourceGroups := make([]string, 0, len(p.UserIdGroupPairs))
		for _, g := range p.UserIdGroupPairs {
			sourceGroups = append(sourceGroups, aws.ToString(g.GroupId))
		}

		// Use the description from the first CIDR or group pair, if any.
		desc := ""
		if len(p.IpRanges) > 0 {
			desc = aws.ToString(p.IpRanges[0].Description)
		} else if len(p.UserIdGroupPairs) > 0 {
			desc = aws.ToString(p.UserIdGroupPairs[0].Description)
		}

		fromPort := int32(0)
		toPort := int32(0)
		if p.FromPort != nil {
			fromPort = *p.FromPort
		}
		if p.ToPort != nil {
			toPort = *p.ToPort
		}

		rules = append(rules, SGRule{
			Protocol:     proto,
			FromPort:     fromPort,
			ToPort:       toPort,
			CIDRs:        cidrs,
			SourceGroups: sourceGroups,
			Description:  desc,
		})
	}
	return rules
}

// ParsePodENIAnnotation unmarshals the JSON value of the
// vpc.amazonaws.com/pod-eni annotation into a slice of PodENIAnnotation.
func ParsePodENIAnnotation(annotation string) ([]PodENIAnnotation, error) {
	var result []PodENIAnnotation
	if err := json.Unmarshal([]byte(annotation), &result); err != nil {
		return nil, fmt.Errorf("failed to parse pod ENI annotation: %w", err)
	}
	return result, nil
}

// GetENISecurityGroups returns the security group IDs attached to the given
// ENI by filtering DescribeNetworkInterfaces by network-interface-id.
func GetENISecurityGroups(ctx context.Context, api EC2API, eniID string) ([]string, error) {
	out, err := api.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("network-interface-id"), Values: []string{eniID}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeNetworkInterfaces (eni %s): %w", eniID, err)
	}
	if len(out.NetworkInterfaces) == 0 {
		return nil, fmt.Errorf("ENI %q not found", eniID)
	}

	eni := out.NetworkInterfaces[0]
	groupIDs := make([]string, 0, len(eni.Groups))
	for _, g := range eni.Groups {
		groupIDs = append(groupIDs, aws.ToString(g.GroupId))
	}
	return groupIDs, nil
}

// GetNodePrimaryENISecurityGroups returns the security group IDs for the
// primary ENI (device index 0) of the given EC2 instance.
func GetNodePrimaryENISecurityGroups(ctx context.Context, api EC2API, instanceID string) ([]string, error) {
	out, err := api.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.instance-id"), Values: []string{instanceID}},
			{Name: aws.String("attachment.device-index"), Values: []string{"0"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeNetworkInterfaces (instance %s): %w", instanceID, err)
	}
	if len(out.NetworkInterfaces) == 0 {
		return nil, fmt.Errorf("primary ENI for instance %q not found", instanceID)
	}

	eni := out.NetworkInterfaces[0]
	groupIDs := make([]string, 0, len(eni.Groups))
	for _, g := range eni.Groups {
		groupIDs = append(groupIDs, aws.ToString(g.GroupId))
	}
	return groupIDs, nil
}
