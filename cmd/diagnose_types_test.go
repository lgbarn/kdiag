package cmd

import (
	"strings"
	"testing"

	"github.com/lgbarn/kdiag/pkg/dns"
	"github.com/lgbarn/kdiag/pkg/netpol"
)

func TestComputeSummary(t *testing.T) {
	tests := []struct {
		name   string
		checks []DiagnoseCheckResult
		want   DiagnoseSummary
	}{
		{
			name:   "empty slice returns all zeros",
			checks: []DiagnoseCheckResult{},
			want:   DiagnoseSummary{Total: 0},
		},
		{
			name: "mixed severities",
			checks: []DiagnoseCheckResult{
				{Severity: SeverityPass},
				{Severity: SeverityPass},
				{Severity: SeverityWarn},
				{Severity: SeverityFail},
				{Severity: SeverityError},
				{Severity: SeveritySkipped},
			},
			want: DiagnoseSummary{Total: 6, Pass: 2, Warn: 1, Fail: 1, Error: 1, Skipped: 1},
		},
		{
			name: "all pass",
			checks: []DiagnoseCheckResult{
				{Severity: SeverityPass},
				{Severity: SeverityPass},
				{Severity: SeverityPass},
			},
			want: DiagnoseSummary{Total: 3, Pass: 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSummary(tt.checks)
			if got != tt.want {
				t.Errorf("computeSummary() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInspectSeverity(t *testing.T) {
	tests := []struct {
		name         string
		result       *InspectResult
		wantSeverity string
		wantContains string
	}{
		{
			name: "all running no restarts returns pass",
			result: &InspectResult{
				Containers: []ContainerSummary{
					{Name: "app", State: "Running", RestartCount: 0},
					{Name: "sidecar", State: "Running", RestartCount: 0},
				},
			},
			wantSeverity: SeverityPass,
			wantContains: "running",
		},
		{
			name: "waiting CrashLoopBackOff returns fail",
			result: &InspectResult{
				Containers: []ContainerSummary{
					{Name: "app", State: "Waiting", StateDetail: "CrashLoopBackOff"},
				},
			},
			wantSeverity: SeverityFail,
			wantContains: "CrashLoopBackOff",
		},
		{
			name: "terminated container returns fail",
			result: &InspectResult{
				Containers: []ContainerSummary{
					{Name: "app", State: "Terminated", StateDetail: "OOMKilled"},
				},
			},
			wantSeverity: SeverityFail,
			wantContains: "Terminated",
		},
		{
			name: "running with restarts returns warn",
			result: &InspectResult{
				Containers: []ContainerSummary{
					{Name: "app", State: "Running", RestartCount: 3},
				},
			},
			wantSeverity: SeverityWarn,
			wantContains: "3 restarts",
		},
		{
			name: "no containers returns pass",
			result: &InspectResult{
				Containers: []ContainerSummary{},
			},
			wantSeverity: SeverityPass,
			wantContains: "non-pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, summary := inspectSeverity(tt.result)
			if severity != tt.wantSeverity {
				t.Errorf("inspectSeverity() severity = %q, want %q", severity, tt.wantSeverity)
			}
			if !strings.Contains(summary, tt.wantContains) {
				t.Errorf("inspectSeverity() summary = %q, want it to contain %q", summary, tt.wantContains)
			}
		})
	}
}

func TestCorednsSeverity(t *testing.T) {
	tests := []struct {
		name         string
		pods         []dns.CoreDNSPod
		wantSeverity string
		wantContains string
	}{
		{
			name: "two pods both ready returns pass",
			pods: []dns.CoreDNSPod{
				{Name: "coredns-1", Ready: true},
				{Name: "coredns-2", Ready: true},
			},
			wantSeverity: SeverityPass,
			wantContains: "2",
		},
		{
			name: "one ready one not ready returns warn",
			pods: []dns.CoreDNSPod{
				{Name: "coredns-1", Ready: true},
				{Name: "coredns-2", Ready: false},
			},
			wantSeverity: SeverityWarn,
			wantContains: "coredns-2",
		},
		{
			name:         "zero pods returns fail",
			pods:         []dns.CoreDNSPod{},
			wantSeverity: SeverityFail,
			wantContains: "no CoreDNS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, summary := corednsSeverity(tt.pods)
			if severity != tt.wantSeverity {
				t.Errorf("corednsSeverity() severity = %q, want %q", severity, tt.wantSeverity)
			}
			if !strings.Contains(summary, tt.wantContains) {
				t.Errorf("corednsSeverity() summary = %q, want it to contain %q", summary, tt.wantContains)
			}
		})
	}
}

func TestNetpolSeverity(t *testing.T) {
	tests := []struct {
		name         string
		result       netpol.NetpolResult
		wantSeverity string
		wantContains string
	}{
		{
			name:         "zero policies returns pass with 0",
			result:       netpol.NetpolResult{Pod: "myapp", Policies: []netpol.PolicySummary{}},
			wantSeverity: SeverityPass,
			wantContains: "0",
		},
		{
			name: "three policies returns pass with 3",
			result: netpol.NetpolResult{
				Pod: "myapp",
				Policies: []netpol.PolicySummary{
					{Name: "allow-ingress"},
					{Name: "deny-egress"},
					{Name: "allow-dns"},
				},
			},
			wantSeverity: SeverityPass,
			wantContains: "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, summary := netpolSeverity(tt.result)
			if severity != tt.wantSeverity {
				t.Errorf("netpolSeverity() severity = %q, want %q", severity, tt.wantSeverity)
			}
			if !strings.Contains(summary, tt.wantContains) {
				t.Errorf("netpolSeverity() summary = %q, want it to contain %q", summary, tt.wantContains)
			}
		})
	}
}

func TestCniSeverity(t *testing.T) {
	tests := []struct {
		name         string
		dsHealthy    bool
		exhausted    int
		wantSeverity string
		wantContains string
	}{
		{
			name:         "healthy ds no exhausted returns pass",
			dsHealthy:    true,
			exhausted:    0,
			wantSeverity: SeverityPass,
			wantContains: "0",
		},
		{
			name:         "unhealthy ds no exhausted returns warn",
			dsHealthy:    false,
			exhausted:    0,
			wantSeverity: SeverityWarn,
			wantContains: "DaemonSet",
		},
		{
			name:         "healthy ds with exhausted returns fail",
			dsHealthy:    true,
			exhausted:    2,
			wantSeverity: SeverityFail,
			wantContains: "2",
		},
		{
			name:         "unhealthy ds with exhausted returns fail (fail takes precedence)",
			dsHealthy:    false,
			exhausted:    1,
			wantSeverity: SeverityFail,
			wantContains: "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, summary := cniSeverity(tt.dsHealthy, tt.exhausted)
			if severity != tt.wantSeverity {
				t.Errorf("cniSeverity() severity = %q, want %q", severity, tt.wantSeverity)
			}
			if !strings.Contains(summary, tt.wantContains) {
				t.Errorf("cniSeverity() summary = %q, want it to contain %q", summary, tt.wantContains)
			}
		})
	}
}

func TestSgSeverity(t *testing.T) {
	tests := []struct {
		name         string
		sgCount      int
		wantSeverity string
		wantContains string
	}{
		{
			name:         "three security groups returns pass",
			sgCount:      3,
			wantSeverity: SeverityPass,
			wantContains: "3",
		},
		{
			name:         "zero security groups returns pass",
			sgCount:      0,
			wantSeverity: SeverityPass,
			wantContains: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, summary := sgSeverity(tt.sgCount)
			if severity != tt.wantSeverity {
				t.Errorf("sgSeverity() severity = %q, want %q", severity, tt.wantSeverity)
			}
			if !strings.Contains(summary, tt.wantContains) {
				t.Errorf("sgSeverity() summary = %q, want it to contain %q", summary, tt.wantContains)
			}
		})
	}
}
