package cmd

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func strPtr(s string) *string { return &s }

func TestDetectIngressController(t *testing.T) {
	tests := []struct {
		name       string
		className  *string
		annotation string
		want       string
	}{
		{"alb class", strPtr("alb"), "", "alb"},
		{"nginx class", strPtr("nginx"), "", "nginx"},
		{"alb annotation", nil, "alb", "alb"},
		{"nginx annotation", nil, "nginx", "nginx"},
		{"unknown class", strPtr("traefik"), "", "traefik"},
		{"no class", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ingress := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
				Spec:       networkingv1.IngressSpec{IngressClassName: tt.className},
			}
			if tt.annotation != "" {
				ingress.Annotations["kubernetes.io/ingress.class"] = tt.annotation
			}
			got := detectIngressController(ingress)
			if got != tt.want {
				t.Errorf("detectIngressController() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCountReadyEndpoints(t *testing.T) {
	tests := []struct {
		name string
		ep   *corev1.Endpoints
		want int
	}{
		{"no subsets", &corev1.Endpoints{}, 0},
		{"one subset with 3 ready", &corev1.Endpoints{
			Subsets: []corev1.EndpointSubset{
				{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}, {IP: "10.0.0.2"}, {IP: "10.0.0.3"}}},
			},
		}, 3},
		{"two subsets", &corev1.Endpoints{
			Subsets: []corev1.EndpointSubset{
				{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}},
				{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.2"}, {IP: "10.0.0.3"}}},
			},
		}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countReadyEndpoints(tt.ep)
			if got != tt.want {
				t.Errorf("countReadyEndpoints() = %d, want %d", got, tt.want)
			}
		})
	}
}
