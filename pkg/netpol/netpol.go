// Package netpol provides utilities for listing and summarising Kubernetes
// NetworkPolicy resources relative to a target pod.
package netpol

import (
	"fmt"
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// NetpolResult holds all matching NetworkPolicy summaries for a pod.
type NetpolResult struct {
	Pod      string          `json:"pod"`
	Policies []PolicySummary `json:"policies"`
}

// PolicySummary is a human-readable representation of a NetworkPolicy.
type PolicySummary struct {
	Name        string        `json:"name"`
	Namespace   string        `json:"namespace"`
	PodSelector string        `json:"pod_selector"`
	PolicyTypes []string      `json:"policy_types"`
	Ingress     []RuleSummary `json:"ingress,omitempty"`
	Egress      []RuleSummary `json:"egress,omitempty"`
}

// RuleSummary describes a single ingress or egress rule.
type RuleSummary struct {
	Ports    []string `json:"ports,omitempty"`
	From     []string `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	IPBlocks []string `json:"ip_blocks,omitempty"`
}

// MatchingPolicies returns the subset of policies whose PodSelector matches
// the supplied pod labels. An empty selector ({}) matches all pods.
func MatchingPolicies(policies []networkingv1.NetworkPolicy, podLabels map[string]string) ([]networkingv1.NetworkPolicy, error) {
	var matched []networkingv1.NetworkPolicy
	for _, policy := range policies {
		sel, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil {
			return nil, fmt.Errorf("policy %s/%s has invalid pod selector: %w", policy.Namespace, policy.Name, err)
		}
		if sel.Matches(labels.Set(podLabels)) {
			matched = append(matched, policy)
		}
	}
	return matched, nil
}

// SummarizePolicy converts a NetworkPolicy into a PolicySummary.
func SummarizePolicy(policy networkingv1.NetworkPolicy) PolicySummary {
	summary := PolicySummary{
		Name:        policy.Name,
		Namespace:   policy.Namespace,
		PodSelector: FormatSelector(&policy.Spec.PodSelector),
	}

	for _, pt := range policy.Spec.PolicyTypes {
		summary.PolicyTypes = append(summary.PolicyTypes, string(pt))
	}

	for _, rule := range policy.Spec.Ingress {
		rs := summariseIngressRule(rule)
		summary.Ingress = append(summary.Ingress, rs)
	}

	for _, rule := range policy.Spec.Egress {
		rs := summariseEgressRule(rule)
		summary.Egress = append(summary.Egress, rs)
	}

	return summary
}

// FormatSelector converts a LabelSelector to a readable string.
// Returns "<all>" for nil or empty selectors.
func FormatSelector(sel *metav1.LabelSelector) string {
	if sel == nil {
		return "<all>"
	}
	if len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		return "<all pods>"
	}

	var parts []string

	// MatchLabels — sort for deterministic output.
	keys := make([]string, 0, len(sel.MatchLabels))
	for k := range sel.MatchLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+sel.MatchLabels[k])
	}

	// MatchExpressions.
	for _, expr := range sel.MatchExpressions {
		parts = append(parts, fmt.Sprintf("%s %s [%s]", expr.Key, expr.Operator, strings.Join(expr.Values, ",")))
	}

	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func summarisePorts(ports []networkingv1.NetworkPolicyPort) []string {
	if len(ports) == 0 {
		return []string{"<all ports>"}
	}
	var result []string
	for _, p := range ports {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		port := "<all>"
		if p.Port != nil {
			port = p.Port.String()
		}
		result = append(result, proto+"/"+port)
	}
	return result
}

// summarisePeerList is the shared implementation for both summarisePeers and
// summariseToPeers. fallbackLabel is returned as the sole entry in the first
// return value when peers is empty (e.g. "<all sources>" or "<all destinations>").
func summarisePeerList(peers []networkingv1.NetworkPolicyPeer, fallbackLabel string) (entries []string, ipBlocks []string) {
	if len(peers) == 0 {
		return []string{fallbackLabel}, nil
	}
	for _, peer := range peers {
		if peer.PodSelector != nil {
			entries = append(entries, "pods: "+FormatSelector(peer.PodSelector))
		}
		if peer.NamespaceSelector != nil {
			entries = append(entries, "namespaces: "+FormatSelector(peer.NamespaceSelector))
		}
		if peer.IPBlock != nil {
			entry := "ipBlock: " + peer.IPBlock.CIDR
			if len(peer.IPBlock.Except) > 0 {
				entry += " except [" + strings.Join(peer.IPBlock.Except, ",") + "]"
			}
			ipBlocks = append(ipBlocks, entry)
		}
	}
	return entries, ipBlocks
}

func summarisePeers(peers []networkingv1.NetworkPolicyPeer) (from []string, ipBlocks []string) {
	return summarisePeerList(peers, "<all sources>")
}

func summariseToPeers(peers []networkingv1.NetworkPolicyPeer) (to []string, ipBlocks []string) {
	return summarisePeerList(peers, "<all destinations>")
}

func summariseIngressRule(rule networkingv1.NetworkPolicyIngressRule) RuleSummary {
	rs := RuleSummary{
		Ports: summarisePorts(rule.Ports),
	}
	from, ipBlocks := summarisePeers(rule.From)
	rs.From = from
	rs.IPBlocks = ipBlocks
	return rs
}

func summariseEgressRule(rule networkingv1.NetworkPolicyEgressRule) RuleSummary {
	rs := RuleSummary{
		Ports: summarisePorts(rule.Ports),
	}
	to, ipBlocks := summariseToPeers(rule.To)
	rs.To = to
	rs.IPBlocks = ipBlocks
	return rs
}
