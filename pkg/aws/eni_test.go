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

// ----------------------------------------------------------------------------
// TestComputeNodeUtilization
// ----------------------------------------------------------------------------

func m5LargeMock() func(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return func(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
		return &ec2.DescribeInstanceTypesOutput{
			InstanceTypes: []types.InstanceTypeInfo{
				{
					InstanceType: types.InstanceType("m5.large"),
					NetworkInfo: &types.NetworkInfo{
						MaximumNetworkInterfaces:  ptrInt32(3),
						Ipv4AddressesPerInterface: ptrInt32(10),
					},
				},
			},
		}, nil
	}
}

func fixedIPsMock(totalIPs int) func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
		devIdx := int32(0)
		return &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []types.NetworkInterface{
				{
					NetworkInterfaceId: ptrStr("eni-001"),
					Attachment:         &types.NetworkInterfaceAttachment{DeviceIndex: &devIdx},
					PrivateIpAddresses: make([]types.NetworkInterfacePrivateIpAddress, totalIPs),
				},
			},
		}, nil
	}
}

func TestComputeNodeUtilization_OK(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	// 2 ENIs, 5 IPs total (2+3), maxENIs=3, maxIPsPerENI=10 → max=30, pct=16
	mock := &mockEC2API{
		describeInstanceTypes: m5LargeMock(),
		describeNetworkInterfaces: func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			devIdx0, devIdx1 := int32(0), int32(1)
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []types.NetworkInterface{
					{
						NetworkInterfaceId: ptrStr("eni-001"),
						Attachment:         &types.NetworkInterfaceAttachment{DeviceIndex: &devIdx0},
						PrivateIpAddresses: make([]types.NetworkInterfacePrivateIpAddress, 2),
					},
					{
						NetworkInterfaceId: ptrStr("eni-002"),
						Attachment:         &types.NetworkInterfaceAttachment{DeviceIndex: &devIdx1},
						PrivateIpAddresses: make([]types.NetworkInterfacePrivateIpAddress, 3),
					},
				},
			}, nil
		},
	}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("len(skipped) = %d, want 0", len(skipped))
	}
	if len(utils) != 1 {
		t.Fatalf("len(utils) = %d, want 1", len(utils))
	}
	u := utils[0]
	if u.Status != "OK" {
		t.Errorf("Status = %q, want OK", u.Status)
	}
	if u.UtilizationPct != 16 {
		t.Errorf("UtilizationPct = %d, want 16", u.UtilizationPct)
	}
	if u.MaxTotalIPs != 30 {
		t.Errorf("MaxTotalIPs = %d, want 30", u.MaxTotalIPs)
	}
	if u.CurrentENIs != 2 {
		t.Errorf("CurrentENIs = %d, want 2", u.CurrentENIs)
	}
	if u.CurrentIPs != 5 {
		t.Errorf("CurrentIPs = %d, want 5", u.CurrentIPs)
	}
}

func TestComputeNodeUtilization_Warning(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	// 21 IPs, maxTotalIPs=30 → pct=70 → WARNING
	mock := &mockEC2API{
		describeInstanceTypes:     m5LargeMock(),
		describeNetworkInterfaces: fixedIPsMock(21),
	}

	utils, _, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(utils) != 1 {
		t.Fatalf("len(utils) = %d, want 1", len(utils))
	}
	if utils[0].Status != "WARNING" {
		t.Errorf("Status = %q, want WARNING", utils[0].Status)
	}
}

func TestComputeNodeUtilization_Exhausted(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	// 26 IPs, maxTotalIPs=30 → pct=86 → EXHAUSTED
	mock := &mockEC2API{
		describeInstanceTypes:     m5LargeMock(),
		describeNetworkInterfaces: fixedIPsMock(26),
	}

	utils, _, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(utils) != 1 {
		t.Fatalf("len(utils) = %d, want 1", len(utils))
	}
	if utils[0].Status != "EXHAUSTED" {
		t.Errorf("Status = %q, want EXHAUSTED", utils[0].Status)
	}
}

func TestComputeNodeUtilization_PrefixDelegation(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	// prefixDelegation=true: maxTotalIPs = 3*10*16 = 480; 26 IPs → pct=5 → OK
	mock := &mockEC2API{
		describeInstanceTypes:     m5LargeMock(),
		describeNetworkInterfaces: fixedIPsMock(26),
	}

	utils, _, err := ComputeNodeUtilization(context.Background(), mock, nodes, true, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(utils) != 1 {
		t.Fatalf("len(utils) = %d, want 1", len(utils))
	}
	u := utils[0]
	if u.Status != "OK" {
		t.Errorf("Status = %q, want OK", u.Status)
	}
	if u.MaxTotalIPs != 480 {
		t.Errorf("MaxTotalIPs = %d, want 480", u.MaxTotalIPs)
	}
}

func TestComputeNodeUtilization_ENIQueryError(t *testing.T) {
	sentinel := errors.New("list-enis failed")
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	mock := &mockEC2API{
		describeInstanceTypes: m5LargeMock(),
		describeNetworkInterfaces: func(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			return nil, sentinel
		},
	}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 0)
	if err != nil {
		t.Fatalf("unexpected terminal error: %v", err)
	}
	if len(utils) != 0 {
		t.Errorf("len(utils) = %d, want 0", len(utils))
	}
	if len(skipped) != 1 {
		t.Fatalf("len(skipped) = %d, want 1", len(skipped))
	}
	if skipped[0].NodeName != "node-1" {
		t.Errorf("skipped[0].NodeName = %q, want node-1", skipped[0].NodeName)
	}
}

func TestComputeNodeUtilization_LimitsError(t *testing.T) {
	sentinel := errors.New("describe-instance-types failed")
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
	}
	mock := &mockEC2API{
		describeInstanceTypes: func(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
			return nil, sentinel
		},
	}

	utils, _, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want to wrap %v", err, sentinel)
	}
	if utils != nil {
		t.Errorf("utils = %v, want nil", utils)
	}
}

func TestComputeNodeUtilization_EmptyInput(t *testing.T) {
	mock := &mockEC2API{}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nil, false, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(utils) != 0 {
		t.Errorf("len(utils) = %d, want 0", len(utils))
	}
	if len(skipped) != 0 {
		t.Errorf("len(skipped) = %d, want 0", len(skipped))
	}
}

func TestComputeNodeUtilization_ConcurrencyOne(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
		{Name: "node-2", InstanceID: "i-002", InstanceType: "m5.large"},
	}

	mock := &mockEC2API{
		describeInstanceTypes:     m5LargeMock(),
		describeNetworkInterfaces: fixedIPsMock(5),
	}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("len(skipped) = %d, want 0", len(skipped))
	}
	if len(utils) != 2 {
		t.Fatalf("len(utils) = %d, want 2", len(utils))
	}
}

func TestComputeNodeUtilization_ConcurrentMultiNode(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
		{Name: "node-2", InstanceID: "i-002", InstanceType: "m5.large"},
		{Name: "node-3", InstanceID: "i-003", InstanceType: "m5.large"},
		{Name: "node-4", InstanceID: "i-004", InstanceType: "m5.large"},
		{Name: "node-5", InstanceID: "i-005", InstanceType: "m5.large"},
	}

	mock := &mockEC2API{
		describeInstanceTypes:     m5LargeMock(),
		describeNetworkInterfaces: fixedIPsMock(2),
	}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("len(skipped) = %d, want 0", len(skipped))
	}
	if len(utils) != 5 {
		t.Fatalf("len(utils) = %d, want 5", len(utils))
	}

	// Verify all 5 node names appear in results (order-independent).
	found := make(map[string]bool)
	for _, u := range utils {
		found[u.NodeName] = true
	}
	for _, n := range nodes {
		if !found[n.Name] {
			t.Errorf("node %q missing from results", n.Name)
		}
	}
}

func TestComputeNodeUtilization_ConcurrentPartialError(t *testing.T) {
	nodes := []NodeInput{
		{Name: "node-1", InstanceID: "i-001", InstanceType: "m5.large"},
		{Name: "node-2", InstanceID: "i-002", InstanceType: "m5.large"},
		{Name: "node-3", InstanceID: "i-003", InstanceType: "m5.large"},
	}

	node2Err := errors.New("node-2 ENI query failed")

	mock := &mockEC2API{
		describeInstanceTypes: m5LargeMock(),
		describeNetworkInterfaces: func(_ context.Context, params *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
			instanceID := params.Filters[0].Values[0]
			if instanceID == "i-002" {
				return nil, node2Err
			}
			devIdx := int32(0)
			return &ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []types.NetworkInterface{
					{
						NetworkInterfaceId: ptrStr("eni-001"),
						Attachment:         &types.NetworkInterfaceAttachment{DeviceIndex: &devIdx},
						PrivateIpAddresses: make([]types.NetworkInterfacePrivateIpAddress, 2),
					},
				},
			}, nil
		},
	}

	utils, skipped, err := ComputeNodeUtilization(context.Background(), mock, nodes, false, 3)
	if err != nil {
		t.Fatalf("unexpected terminal error: %v", err)
	}
	if len(utils) != 2 {
		t.Errorf("len(utils) = %d, want 2", len(utils))
	}
	if len(skipped) != 1 {
		t.Fatalf("len(skipped) = %d, want 1", len(skipped))
	}
	if skipped[0].NodeName != "node-2" {
		t.Errorf("skipped[0].NodeName = %q, want node-2", skipped[0].NodeName)
	}
}
