package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ptr helpers for test data

func ptrStr(s string) *string  { return &s }
func ptrInt32(i int32) *int32  { return &i }

// ----------------------------------------------------------------------------
// TestListNodeENIs
// ----------------------------------------------------------------------------

func TestListNodeENIs_Success(t *testing.T) {
	sg1 := "sg-aaa111"
	sg2 := "sg-bbb222"

	eni1PrivIPs := make([]types.NetworkInterfacePrivateIpAddress, 3)
	eni2PrivIPs := make([]types.NetworkInterfacePrivateIpAddress, 5)

	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, params *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			devIdx0 := int32(0)
			devIdx1 := int32(1)
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []types.NetworkInterface{
					{
						NetworkInterfaceId: ptrStr("eni-000001"),
						Description:        ptrStr("primary"),
						Attachment: &types.NetworkInterfaceAttachment{
							DeviceIndex: &devIdx0,
						},
						Groups:              []types.GroupIdentifier{{GroupId: &sg1}},
						PrivateIpAddresses:  eni1PrivIPs,
					},
					{
						NetworkInterfaceId: ptrStr("eni-000002"),
						Description:        ptrStr("secondary"),
						Attachment: &types.NetworkInterfaceAttachment{
							DeviceIndex: &devIdx1,
						},
						Groups:             []types.GroupIdentifier{{GroupId: &sg2}},
						PrivateIpAddresses: eni2PrivIPs,
					},
				},
			}, nil
		},
	}

	info, err := ListNodeENIs(context.Background(), mock, "i-0abc123def456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.InstanceID != "i-0abc123def456789" {
		t.Errorf("InstanceID = %q, want %q", info.InstanceID, "i-0abc123def456789")
	}
	if len(info.ENIs) != 2 {
		t.Errorf("len(ENIs) = %d, want 2", len(info.ENIs))
	}
	if info.TotalIPs != 8 {
		t.Errorf("TotalIPs = %d, want 8 (3+5)", info.TotalIPs)
	}

	// Spot-check first ENI
	eni := info.ENIs[0]
	if eni.ENIID != "eni-000001" {
		t.Errorf("ENIs[0].ENIID = %q, want eni-000001", eni.ENIID)
	}
	if eni.DeviceIndex != 0 {
		t.Errorf("ENIs[0].DeviceIndex = %d, want 0", eni.DeviceIndex)
	}
	if eni.PrivateIPCount != 3 {
		t.Errorf("ENIs[0].PrivateIPCount = %d, want 3", eni.PrivateIPCount)
	}
	if len(eni.SecurityGroups) != 1 || eni.SecurityGroups[0] != sg1 {
		t.Errorf("ENIs[0].SecurityGroups = %v, want [%s]", eni.SecurityGroups, sg1)
	}
}

func TestListNodeENIs_NoENIs(t *testing.T) {
	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []types.NetworkInterface{},
			}, nil
		},
	}

	info, err := ListNodeENIs(context.Background(), mock, "i-0abc123def456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.ENIs) != 0 {
		t.Errorf("len(ENIs) = %d, want 0", len(info.ENIs))
	}
	if info.TotalIPs != 0 {
		t.Errorf("TotalIPs = %d, want 0", info.TotalIPs)
	}
}

func TestListNodeENIs_APIError(t *testing.T) {
	sentinel := errors.New("describe-network-interfaces failed")
	mock := &mockEC2API{
		describeNetworkInterfaces: func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return nil, sentinel
		},
	}

	_, err := ListNodeENIs(context.Background(), mock, "i-0abc123def456789")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want to wrap %v", err, sentinel)
	}
}

// ----------------------------------------------------------------------------
// TestGetInstanceTypeLimits
// ----------------------------------------------------------------------------

func TestGetInstanceTypeLimits_Success(t *testing.T) {
	mock := &mockEC2API{
		describeInstanceTypes: func(_ context.Context, params *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
			return &ec2.DescribeInstanceTypesOutput{
				InstanceTypes: []types.InstanceTypeInfo{
					{
						InstanceType: types.InstanceType("m5.large"),
						NetworkInfo: &types.NetworkInfo{
							MaximumNetworkInterfaces: ptrInt32(3),
							Ipv4AddressesPerInterface: ptrInt32(10),
						},
					},
					{
						InstanceType: types.InstanceType("t3.small"),
						NetworkInfo: &types.NetworkInfo{
							MaximumNetworkInterfaces: ptrInt32(3),
							Ipv4AddressesPerInterface: ptrInt32(4),
						},
					},
				},
			}, nil
		},
	}

	limits, err := GetInstanceTypeLimits(context.Background(), mock, []string{"m5.large", "t3.small"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(limits) != 2 {
		t.Errorf("len(limits) = %d, want 2", len(limits))
	}

	m5 := limits["m5.large"]
	if m5 == nil {
		t.Fatal("expected m5.large entry, got nil")
	}
	if m5.MaxENIs != 3 {
		t.Errorf("m5.large MaxENIs = %d, want 3", m5.MaxENIs)
	}
	if m5.MaxIPsPerENI != 10 {
		t.Errorf("m5.large MaxIPsPerENI = %d, want 10", m5.MaxIPsPerENI)
	}

	t3 := limits["t3.small"]
	if t3 == nil {
		t.Fatal("expected t3.small entry, got nil")
	}
	if t3.MaxENIs != 3 {
		t.Errorf("t3.small MaxENIs = %d, want 3", t3.MaxENIs)
	}
	if t3.MaxIPsPerENI != 4 {
		t.Errorf("t3.small MaxIPsPerENI = %d, want 4", t3.MaxIPsPerENI)
	}
}

func TestGetInstanceTypeLimits_Empty(t *testing.T) {
	mock := &mockEC2API{}

	limits, err := GetInstanceTypeLimits(context.Background(), mock, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(limits) != 0 {
		t.Errorf("len(limits) = %d, want 0", len(limits))
	}
}
