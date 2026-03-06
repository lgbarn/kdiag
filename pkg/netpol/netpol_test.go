package netpol_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lgbarn/kdiag/pkg/netpol"
)

// helper to build a protocol pointer.
func protoPtr(p corev1.Protocol) *corev1.Protocol { return &p }

// helper to build an IntOrString pointer from an int.
func portPtr(n int) *intstr.IntOrString { v := intstr.FromInt(n); return &v }

// ---------------------------------------------------------------------------
// MatchingPolicies tests
// ---------------------------------------------------------------------------

// TestMatchingPolicies_MatchByLabel: policy with app=web matches a pod that
// has app=web,tier=frontend but not a pod with app=api.
func TestMatchingPolicies_MatchByLabel(t *testing.T) {
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "web-policy", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
		},
	}

	matchingLabels := map[string]string{"app": "web", "tier": "frontend"}
	nonMatchingLabels := map[string]string{"app": "api"}

	matched, err := netpol.MatchingPolicies([]networkingv1.NetworkPolicy{policy}, matchingLabels)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matched) != 1 {
		t.Errorf("expected 1 match for app=web pod, got %d", len(matched))
	}

	notMatched, err := netpol.MatchingPolicies([]networkingv1.NetworkPolicy{policy}, nonMatchingLabels)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notMatched) != 0 {
		t.Errorf("expected 0 matches for app=api pod, got %d", len(notMatched))
	}
}

// TestMatchingPolicies_EmptySelector: an empty PodSelector {} must match any pod.
func TestMatchingPolicies_EmptySelector(t *testing.T) {
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{}, // matches everything
		},
	}

	matched, err := netpol.MatchingPolicies([]networkingv1.NetworkPolicy{policy}, map[string]string{"app": "anything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matched) != 1 {
		t.Errorf("expected 1 match for empty selector, got %d", len(matched))
	}
}

// TestMatchingPolicies_NoMatch: returns an empty slice when nothing matches.
func TestMatchingPolicies_NoMatch(t *testing.T) {
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "db-policy", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "db"},
			},
		},
	}

	matched, err := netpol.MatchingPolicies([]networkingv1.NetworkPolicy{policy}, map[string]string{"app": "web"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matched) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matched))
	}
}

// ---------------------------------------------------------------------------
// SummarizePolicy tests
// ---------------------------------------------------------------------------

// TestSummarizePolicy_Ingress: one ingress rule allowing TCP/80 from app=frontend.
func TestSummarizePolicy_Ingress(t *testing.T) {
	tcp := corev1.ProtocolTCP
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "web-ingress", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &tcp, Port: portPtr(80)},
					},
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "frontend"},
							},
						},
					},
				},
			},
		},
	}

	summary := netpol.SummarizePolicy(policy)

	if summary.Name != "web-ingress" {
		t.Errorf("expected Name=web-ingress, got %q", summary.Name)
	}
	if len(summary.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(summary.Ingress))
	}
	rule := summary.Ingress[0]
	if len(rule.Ports) != 1 || rule.Ports[0] != "TCP/80" {
		t.Errorf("expected port TCP/80, got %v", rule.Ports)
	}
	if len(rule.From) != 1 || !strings.Contains(rule.From[0], "app=frontend") {
		t.Errorf("expected From to contain 'app=frontend', got %v", rule.From)
	}
}

// TestSummarizePolicy_Egress: one egress rule allowing UDP/53 to ipBlock 10.0.0.0/8
// with except 10.1.0.0/16.
func TestSummarizePolicy_Egress(t *testing.T) {
	udp := corev1.ProtocolUDP
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dns-egress", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &udp, Port: portPtr(53)},
					},
					To: []networkingv1.NetworkPolicyPeer{
						{
							IPBlock: &networkingv1.IPBlock{
								CIDR:   "10.0.0.0/8",
								Except: []string{"10.1.0.0/16"},
							},
						},
					},
				},
			},
		},
	}

	summary := netpol.SummarizePolicy(policy)

	if len(summary.Egress) != 1 {
		t.Fatalf("expected 1 egress rule, got %d", len(summary.Egress))
	}
	rule := summary.Egress[0]
	if len(rule.Ports) != 1 || rule.Ports[0] != "UDP/53" {
		t.Errorf("expected port UDP/53, got %v", rule.Ports)
	}
	if len(rule.IPBlocks) != 1 {
		t.Fatalf("expected 1 IPBlock entry, got %d", len(rule.IPBlocks))
	}
	if !strings.Contains(rule.IPBlocks[0], "10.0.0.0/8") {
		t.Errorf("expected IPBlock to contain 10.0.0.0/8, got %q", rule.IPBlocks[0])
	}
	if !strings.Contains(rule.IPBlocks[0], "10.1.0.0/16") {
		t.Errorf("expected IPBlock to contain except 10.1.0.0/16, got %q", rule.IPBlocks[0])
	}
}

// TestSummarizePolicy_AllSources: an ingress rule with nil From produces "<all sources>".
func TestSummarizePolicy_AllSources(t *testing.T) {
	policy := networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all-ingress", Namespace: "default"},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					// nil From means allow from all sources
					From: nil,
				},
			},
		},
	}

	summary := netpol.SummarizePolicy(policy)

	if len(summary.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(summary.Ingress))
	}
	rule := summary.Ingress[0]
	if len(rule.From) != 1 || rule.From[0] != "<all sources>" {
		t.Errorf("expected From=[<all sources>], got %v", rule.From)
	}
	if len(rule.Ports) != 1 || rule.Ports[0] != "<all ports>" {
		t.Errorf("expected Ports=[<all ports>], got %v", rule.Ports)
	}
}
