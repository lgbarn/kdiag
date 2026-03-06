package k8s

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ComputeType represents the compute backend a pod is running on.
type ComputeType string

const (
	// ComputeTypeManaged indicates the pod is running on a managed EC2 node.
	ComputeTypeManaged ComputeType = "managed"

	// ComputeTypeFargate indicates the pod is running on AWS Fargate.
	ComputeTypeFargate ComputeType = "fargate"

	// ComputeTypeAutoMode indicates the node is managed by EKS Auto Mode.
	ComputeTypeAutoMode ComputeType = "auto-mode"
)

// DetectComputeType determines whether a pod is running on Fargate or a
// managed EC2 node. It first checks for the fargate-profile label applied
// by the EKS Fargate admission webhook, then falls back to checking the
// node name prefix used by Fargate virtual nodes.
func DetectComputeType(pod *corev1.Pod) ComputeType {
	if _, ok := pod.Labels["eks.amazonaws.com/fargate-profile"]; ok {
		return ComputeTypeFargate
	}

	if IsFargateNode(pod.Spec.NodeName) {
		return ComputeTypeFargate
	}

	return ComputeTypeManaged
}

// IsFargateNode returns true if the given node name is a Fargate virtual node,
// identified by the "fargate-ip-" prefix that EKS assigns to Fargate nodes.
func IsFargateNode(nodeName string) bool {
	return strings.HasPrefix(nodeName, "fargate-ip-")
}

// IsAutoModeNode returns true when the node carries the EKS Auto Mode label
// (eks.amazonaws.com/compute-type=auto).
func IsAutoModeNode(node *corev1.Node) bool {
	return node.Labels["eks.amazonaws.com/compute-type"] == "auto"
}

// DetectNodeComputeType determines the compute backend of a node.
// Auto Mode is checked first, then Fargate, then Managed.
func DetectNodeComputeType(node *corev1.Node) ComputeType {
	if IsAutoModeNode(node) {
		return ComputeTypeAutoMode
	}
	if IsFargateNode(node.Name) {
		return ComputeTypeFargate
	}
	return ComputeTypeManaged
}
