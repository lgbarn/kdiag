package cmd

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

// IngressResult holds the structured output of an ingress inspection.
type IngressResult struct {
	Name       string              `json:"name"`
	Namespace  string              `json:"namespace"`
	Class      string              `json:"class"`
	Controller string              `json:"controller"`
	Rules      []IngressRuleResult `json:"rules"`
	TLS        []IngressTLSResult  `json:"tls"`
	CtrlHealth string              `json:"controller_health,omitempty"`
}

// IngressRuleResult holds the check result for a single ingress rule path.
type IngressRuleResult struct {
	Host           string `json:"host"`
	Path           string `json:"path"`
	ServiceName    string `json:"service_name"`
	ServicePort    string `json:"service_port"`
	ServiceExists  bool   `json:"service_exists"`
	EndpointsReady int    `json:"endpoints_ready"`
}

// IngressTLSResult holds the check result for a TLS secret reference.
type IngressTLSResult struct {
	SecretName string   `json:"secret_name"`
	Hosts      []string `json:"hosts"`
	Exists     bool     `json:"exists"`
}

// detectIngressController returns the controller type from the Ingress spec.
func detectIngressController(ingress *networkingv1.Ingress) string {
	if ingress.Spec.IngressClassName != nil {
		return *ingress.Spec.IngressClassName
	}
	if v, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		return v
	}
	return ""
}

// countReadyEndpoints counts the number of ready addresses across all subsets.
func countReadyEndpoints(ep *corev1.Endpoints) int {
	count := 0
	for _, subset := range ep.Subsets {
		count += len(subset.Addresses)
	}
	return count
}
