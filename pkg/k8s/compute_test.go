package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDetectComputeType_FargateByLabel(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"eks.amazonaws.com/fargate-profile": "my-profile",
			},
		},
	}

	got := DetectComputeType(pod)
	if got != ComputeTypeFargate {
		t.Errorf("expected ComputeTypeFargate, got %q", got)
	}
}

func TestDetectComputeType_FargateByNodeName(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeName: "fargate-ip-192-168-1-1.us-east-1.compute.internal",
		},
	}

	got := DetectComputeType(pod)
	if got != ComputeTypeFargate {
		t.Errorf("expected ComputeTypeFargate, got %q", got)
	}
}

func TestDetectComputeType_Managed(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeName: "ip-10-0-1-2.us-west-2.compute.internal",
		},
	}

	got := DetectComputeType(pod)
	if got != ComputeTypeManaged {
		t.Errorf("expected ComputeTypeManaged, got %q", got)
	}
}

func TestIsFargateNode_FargatePrefix(t *testing.T) {
	got := IsFargateNode("fargate-ip-10-0-1-2.us-west-2.compute.internal")
	if !got {
		t.Error("expected IsFargateNode to return true for fargate-ip- prefix")
	}
}

func TestIsFargateNode_RegularPrefix(t *testing.T) {
	got := IsFargateNode("ip-10-0-1-2.us-west-2.compute.internal")
	if got {
		t.Error("expected IsFargateNode to return false for non-fargate node name")
	}
}
