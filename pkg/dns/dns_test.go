package dns_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/dns"
)

// TestBuildFQDN verifies that bare names get the cluster-local suffix appended
// while names that already contain dots are returned unchanged.
func TestBuildFQDN(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      string
	}{
		{
			name:      "nginx",
			namespace: "default",
			want:      "nginx.default.svc.cluster.local",
		},
		{
			name:      "foo.bar.com",
			namespace: "default",
			want:      "foo.bar.com",
		},
		{
			name:      "my-svc",
			namespace: "kube-system",
			want:      "my-svc.kube-system.svc.cluster.local",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/"+tc.namespace, func(t *testing.T) {
			got := dns.BuildFQDN(tc.name, tc.namespace)
			if got != tc.want {
				t.Errorf("BuildFQDN(%q, %q) = %q; want %q", tc.name, tc.namespace, got, tc.want)
			}
		})
	}
}

// realDigOutput is a representative dig output with two A records.
const realDigOutput = `
; <<>> DiG 9.18.1 <<>> nginx.default.svc.cluster.local @10.96.0.10 +noall +answer +stats
;; global options: +cmd
nginx.default.svc.cluster.local. 5	IN	A	10.0.1.100
nginx.default.svc.cluster.local. 5	IN	A	10.0.1.101

;; Query time: 3 msec
;; SERVER: 10.96.0.10#53(10.96.0.10) (UDP)
;; WHEN: Thu Jan 01 00:00:00 UTC 2026
;; MSG SIZE  rcvd: 90
`

// nxdomainDigOutput simulates a NXDOMAIN response.
const nxdomainDigOutput = `
; <<>> DiG 9.18.1 <<>> notexist.default.svc.cluster.local @10.96.0.10 +noall +answer +stats
;; global options: +cmd

;; Query time: 1 msec
;; SERVER: 10.96.0.10#53(10.96.0.10) (UDP)
;; ->>HEADER<<- opcode: QUERY, status: NXDOMAIN, id: 12345
;; MSG SIZE  rcvd: 50
`

// aaaaDigOutput simulates a response with a single AAAA record.
const aaaaDigOutput = `
; <<>> DiG 9.18.1 <<>> ipv6svc.default.svc.cluster.local @10.96.0.10 +noall +answer +stats
;; global options: +cmd
ipv6svc.default.svc.cluster.local. 5	IN	AAAA	fd00::1

;; Query time: 2 msec
;; SERVER: 10.96.0.10#53(10.96.0.10) (UDP)
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 42
;; MSG SIZE  rcvd: 75
`

// noErrorEmptyAnswerOutput simulates a NOERROR response with no answer records
// (e.g. a negative cache hit or CNAME-only response).
const noErrorEmptyAnswerOutput = `
; <<>> DiG 9.18.1 <<>> cname-only.default.svc.cluster.local @10.96.0.10 +noall +answer +stats
;; global options: +cmd

;; Query time: 1 msec
;; SERVER: 10.96.0.10#53(10.96.0.10) (UDP)
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 99
;; MSG SIZE  rcvd: 42
`

// TestParseDigOutput verifies IP extraction, query time parsing, and error
// cases for NXDOMAIN and empty output.
func TestParseDigOutput(t *testing.T) {
	t.Run("two A records", func(t *testing.T) {
		ips, ms, err := dns.ParseDigOutput(realDigOutput)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ips) != 2 {
			t.Fatalf("expected 2 IPs, got %d: %v", len(ips), ips)
		}
		if ips[0] != "10.0.1.100" {
			t.Errorf("expected first IP 10.0.1.100, got %q", ips[0])
		}
		if ips[1] != "10.0.1.101" {
			t.Errorf("expected second IP 10.0.1.101, got %q", ips[1])
		}
		if ms != 3 {
			t.Errorf("expected query time 3 ms, got %d", ms)
		}
	})

	t.Run("NXDOMAIN returns error", func(t *testing.T) {
		_, _, err := dns.ParseDigOutput(nxdomainDigOutput)
		if err == nil {
			t.Fatal("expected error for NXDOMAIN, got nil")
		}
		if !strings.Contains(err.Error(), "NXDOMAIN") {
			t.Errorf("expected error to mention NXDOMAIN, got: %v", err)
		}
	})

	t.Run("AAAA record", func(t *testing.T) {
		ips, ms, err := dns.ParseDigOutput(aaaaDigOutput)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ips) != 1 {
			t.Fatalf("expected 1 IP, got %d: %v", len(ips), ips)
		}
		if ips[0] != "fd00::1" {
			t.Errorf("expected IP fd00::1, got %q", ips[0])
		}
		if ms != 2 {
			t.Errorf("expected query time 2 ms, got %d", ms)
		}
	})

	t.Run("NOERROR with empty answer returns nil error", func(t *testing.T) {
		ips, _, err := dns.ParseDigOutput(noErrorEmptyAnswerOutput)
		if err != nil {
			t.Fatalf("expected nil error for NOERROR+empty answer, got: %v", err)
		}
		if len(ips) != 0 {
			t.Errorf("expected empty IP slice, got %v", ips)
		}
	})

	t.Run("empty output returns error", func(t *testing.T) {
		_, _, err := dns.ParseDigOutput("")
		if err == nil {
			t.Fatal("expected error for empty output, got nil")
		}
	})
}

// TestBuildDigCommand verifies the dig command slice construction.
func TestBuildDigCommand(t *testing.T) {
	t.Run("with server IP returns 6 elements", func(t *testing.T) {
		cmd := dns.BuildDigCommand("nginx.default.svc.cluster.local", "10.96.0.10")
		if len(cmd) != 6 {
			t.Errorf("expected 6 elements, got %d: %v", len(cmd), cmd)
		}
		if cmd[0] != "dig" {
			t.Errorf("expected cmd[0]=dig, got %q", cmd[0])
		}
		if cmd[2] != "@10.96.0.10" {
			t.Errorf("expected cmd[2]=@10.96.0.10, got %q", cmd[2])
		}
	})

	t.Run("without server IP returns 5 elements", func(t *testing.T) {
		cmd := dns.BuildDigCommand("nginx.default.svc.cluster.local", "")
		if len(cmd) != 5 {
			t.Errorf("expected 5 elements, got %d: %v", len(cmd), cmd)
		}
		// Make sure no @ argument is present
		for _, arg := range cmd {
			if strings.HasPrefix(arg, "@") {
				t.Errorf("expected no @ argument, found %q", arg)
			}
		}
	})
}

// TestEvaluateCoreDNSPods verifies that the Ready field is correctly computed
// from container statuses.
func TestEvaluateCoreDNSPods(t *testing.T) {
	allReady := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns-abc"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true},
				{Ready: true},
			},
		},
	}

	notReady := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns-xyz"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true},
				{Ready: false},
			},
		},
	}

	results := dns.EvaluateCoreDNSPods([]corev1.Pod{allReady, notReady})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Name != "coredns-abc" {
		t.Errorf("expected name coredns-abc, got %q", results[0].Name)
	}
	if !results[0].Ready {
		t.Error("expected pod coredns-abc to be Ready=true")
	}

	if results[1].Name != "coredns-xyz" {
		t.Errorf("expected name coredns-xyz, got %q", results[1].Name)
	}
	if results[1].Ready {
		t.Error("expected pod coredns-xyz to be Ready=false")
	}
}
