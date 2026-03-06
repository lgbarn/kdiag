package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// TestGetSecurityGroupDetails
// ---------------------------------------------------------------------------

func TestGetSecurityGroupDetails_Success(t *testing.T) {
	mock := &mockEC2API{
		describeSecurityGroups: func(_ context.Context, params *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
			return &ec2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{
					{
						GroupId:     aws.String("sg-abc123"),
						GroupName:   aws.String("my-sg"),
						Description: aws.String("test security group"),
						IpPermissions: []ec2types.IpPermission{
							{
								// TCP 443 from 10.0.0.0/8
								IpProtocol: aws.String("tcp"),
								FromPort:   aws.Int32(443),
								ToPort:     aws.Int32(443),
								IpRanges: []ec2types.IpRange{
									{CidrIp: aws.String("10.0.0.0/8"), Description: aws.String("internal")},
								},
							},
							{
								// all from sg-xxx (UserIdGroupPair)
								IpProtocol: aws.String("-1"),
								UserIdGroupPairs: []ec2types.UserIdGroupPair{
									{GroupId: aws.String("sg-xxx"), Description: aws.String("from peer sg")},
								},
							},
						},
						IpPermissionsEgress: []ec2types.IpPermission{
							{
								// all to 0.0.0.0/0
								IpProtocol: aws.String("-1"),
								IpRanges: []ec2types.IpRange{
									{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("")},
								},
							},
						},
					},
				},
			}, nil
		},
	}

	ctx := context.Background()
	details, err := GetSecurityGroupDetails(ctx, mock, []string{"sg-abc123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("expected 1 SG, got %d", len(details))
	}

	sg := details[0]
	if sg.GroupID != "sg-abc123" {
		t.Errorf("GroupID = %q, want %q", sg.GroupID, "sg-abc123")
	}
	if sg.GroupName != "my-sg" {
		t.Errorf("GroupName = %q, want %q", sg.GroupName, "my-sg")
	}
	if sg.Description != "test security group" {
		t.Errorf("Description = %q, want %q", sg.Description, "test security group")
	}

	// Ingress rules: TCP 443 + all (from -1 mapped to "all")
	if len(sg.IngressRules) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(sg.IngressRules))
	}
	tcpRule := sg.IngressRules[0]
	if tcpRule.Protocol != "tcp" {
		t.Errorf("ingress[0].Protocol = %q, want %q", tcpRule.Protocol, "tcp")
	}
	if tcpRule.FromPort != 443 || tcpRule.ToPort != 443 {
		t.Errorf("ingress[0] ports = %d-%d, want 443-443", tcpRule.FromPort, tcpRule.ToPort)
	}
	if len(tcpRule.CIDRs) != 1 || tcpRule.CIDRs[0] != "10.0.0.0/8" {
		t.Errorf("ingress[0].CIDRs = %v, want [10.0.0.0/8]", tcpRule.CIDRs)
	}

	allRule := sg.IngressRules[1]
	if allRule.Protocol != "all" {
		t.Errorf("ingress[1].Protocol = %q, want %q", allRule.Protocol, "all")
	}
	if len(allRule.SourceGroups) != 1 || allRule.SourceGroups[0] != "sg-xxx" {
		t.Errorf("ingress[1].SourceGroups = %v, want [sg-xxx]", allRule.SourceGroups)
	}

	// Egress: 1 rule, all to 0.0.0.0/0
	if len(sg.EgressRules) != 1 {
		t.Fatalf("expected 1 egress rule, got %d", len(sg.EgressRules))
	}
	egress := sg.EgressRules[0]
	if egress.Protocol != "all" {
		t.Errorf("egress[0].Protocol = %q, want %q", egress.Protocol, "all")
	}
	if len(egress.CIDRs) != 1 || egress.CIDRs[0] != "0.0.0.0/0" {
		t.Errorf("egress[0].CIDRs = %v, want [0.0.0.0/0]", egress.CIDRs)
	}
}

func TestGetSecurityGroupDetails_NotFound(t *testing.T) {
	mock := &mockEC2API{
		describeSecurityGroups: func(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
			return &ec2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{},
			}, nil
		},
	}

	ctx := context.Background()
	details, err := GetSecurityGroupDetails(ctx, mock, []string{"sg-nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(details) != 0 {
		t.Errorf("expected 0 SGs, got %d", len(details))
	}
}

func TestGetSecurityGroupDetails_APIError(t *testing.T) {
	mock := &mockEC2API{
		describeSecurityGroups: func(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
			return nil, errors.New("access denied")
		},
	}

	ctx := context.Background()
	_, err := GetSecurityGroupDetails(ctx, mock, []string{"sg-abc"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestParsePodENIAnnotation
// ---------------------------------------------------------------------------

func TestParsePodENIAnnotation_Valid(t *testing.T) {
	annotation := `[{"eniId":"eni-0abc123","privateIp":"192.168.1.10","vlanId":1,"subnetCidr":"192.168.0.0/16"}]`
	result, err := ParsePodENIAnnotation(annotation)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	entry := result[0]
	if entry.ENIID != "eni-0abc123" {
		t.Errorf("ENIID = %q, want %q", entry.ENIID, "eni-0abc123")
	}
	if entry.PrivateIP != "192.168.1.10" {
		t.Errorf("PrivateIP = %q, want %q", entry.PrivateIP, "192.168.1.10")
	}
	if entry.VlanID != 1 {
		t.Errorf("VlanID = %d, want 1", entry.VlanID)
	}
	if entry.SubnetCIDR != "192.168.0.0/16" {
		t.Errorf("SubnetCIDR = %q, want %q", entry.SubnetCIDR, "192.168.0.0/16")
	}
}

func TestParsePodENIAnnotation_Invalid(t *testing.T) {
	_, err := ParsePodENIAnnotation("not-json{{{")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParsePodENIAnnotation_Empty(t *testing.T) {
	result, err := ParsePodENIAnnotation("[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(result))
	}
}

// ---------------------------------------------------------------------------
// TestGetENISecurityGroups
// ---------------------------------------------------------------------------

func TestGetENISecurityGroups_Success(t *testing.T) {
	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, params *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []ec2types.NetworkInterface{
					{
						Groups: []ec2types.GroupIdentifier{
							{GroupId: aws.String("sg-111aaa")},
							{GroupId: aws.String("sg-222bbb")},
						},
					},
				},
			}, nil
		},
	}

	ctx := context.Background()
	groupIDs, err := GetENISecurityGroups(ctx, mock, "eni-0abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groupIDs) != 2 {
		t.Fatalf("expected 2 group IDs, got %d", len(groupIDs))
	}
	if groupIDs[0] != "sg-111aaa" {
		t.Errorf("groupIDs[0] = %q, want %q", groupIDs[0], "sg-111aaa")
	}
	if groupIDs[1] != "sg-222bbb" {
		t.Errorf("groupIDs[1] = %q, want %q", groupIDs[1], "sg-222bbb")
	}
}

func TestGetENISecurityGroups_NotFound(t *testing.T) {
	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []ec2types.NetworkInterface{},
			}, nil
		},
	}

	ctx := context.Background()
	_, err := GetENISecurityGroups(ctx, mock, "eni-nonexistent")
	if err == nil {
		t.Fatal("expected error for missing ENI, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestGetNodePrimaryENISecurityGroups
// ---------------------------------------------------------------------------

func TestGetNodePrimaryENISecurityGroups_Success(t *testing.T) {
	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, params *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []ec2types.NetworkInterface{
					{
						Groups: []ec2types.GroupIdentifier{
							{GroupId: aws.String("sg-node-primary")},
						},
					},
				},
			}, nil
		},
	}

	ctx := context.Background()
	groupIDs, err := GetNodePrimaryENISecurityGroups(ctx, mock, "i-0abc123def456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groupIDs) != 1 {
		t.Fatalf("expected 1 group ID, got %d", len(groupIDs))
	}
	if groupIDs[0] != "sg-node-primary" {
		t.Errorf("groupIDs[0] = %q, want %q", groupIDs[0], "sg-node-primary")
	}
}
