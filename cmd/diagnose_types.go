package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lgbarn/kdiag/pkg/dns"
	"github.com/lgbarn/kdiag/pkg/netpol"
)

// ErrDiagnoseFail is returned when one or more diagnose checks fail.
var ErrDiagnoseFail = errors.New("diagnose: one or more checks failed")

// DiagnoseReport is the top-level structured result for the diagnose command.
type DiagnoseReport struct {
	Pod       string                `json:"pod"`
	Namespace string                `json:"namespace"`
	IsEKS     bool                  `json:"is_eks"`
	Checks    []DiagnoseCheckResult `json:"checks"`
	Summary   DiagnoseSummary       `json:"summary"`
}

// DiagnoseCheckResult holds the outcome of a single diagnostic check.
type DiagnoseCheckResult struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Error    string `json:"error,omitempty"`
}

// DiagnoseSummary aggregates check counts by severity bucket.
type DiagnoseSummary struct {
	Total   int `json:"total"`
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Error   int `json:"error"`
	Skipped int `json:"skipped"`
}

// computeSummary builds a DiagnoseSummary by counting each severity bucket.
func computeSummary(checks []DiagnoseCheckResult) DiagnoseSummary {
	s := DiagnoseSummary{Total: len(checks)}
	for _, c := range checks {
		switch c.Severity {
		case "pass":
			s.Pass++
		case "warn":
			s.Warn++
		case "fail":
			s.Fail++
		case "error":
			s.Error++
		case "skipped":
			s.Skipped++
		}
	}
	return s
}

// inspectSeverity derives a severity and human-readable summary from an
// InspectResult. It returns "fail" if any container is in CrashLoopBackOff or
// Terminated state, "warn" if any container has restarts, and "pass" otherwise.
func inspectSeverity(result *InspectResult) (severity, summary string) {
	if len(result.Containers) == 0 {
		return "pass", "no containers (non-pod resource)"
	}

	var failing []string
	var restarting []string

	for _, c := range result.Containers {
		switch {
		case c.State == "Waiting" && strings.Contains(c.StateDetail, "CrashLoopBackOff"):
			failing = append(failing, fmt.Sprintf("%s (CrashLoopBackOff)", c.Name))
		case c.State == "Terminated":
			failing = append(failing, fmt.Sprintf("%s (Terminated)", c.Name))
		case c.RestartCount > 0:
			restarting = append(restarting, fmt.Sprintf("%s (%d restarts)", c.Name, c.RestartCount))
		}
	}

	if len(failing) > 0 {
		return "fail", fmt.Sprintf("containers failing: %s", strings.Join(failing, ", "))
	}
	if len(restarting) > 0 {
		return "warn", fmt.Sprintf("containers restarting: %s", strings.Join(restarting, ", "))
	}
	return "pass", fmt.Sprintf("all %d container(s) running normally", len(result.Containers))
}

// corednsSeverity derives a severity and summary from a list of CoreDNS pods.
// Returns "fail" if no pods found, "warn" if any pod is not ready, "pass" otherwise.
func corednsSeverity(pods []dns.CoreDNSPod) (severity, summary string) {
	if len(pods) == 0 {
		return "fail", "no CoreDNS pods found"
	}

	var notReady []string
	for _, p := range pods {
		if !p.Ready {
			notReady = append(notReady, p.Name)
		}
	}

	if len(notReady) > 0 {
		return "warn", fmt.Sprintf("%d/%d CoreDNS pod(s) not ready: %s", len(notReady), len(pods), strings.Join(notReady, ", "))
	}
	return "pass", fmt.Sprintf("all %d CoreDNS pod(s) ready", len(pods))
}

// netpolSeverity always returns "pass" with a summary stating how many
// NetworkPolicies matched the pod.
func netpolSeverity(result netpol.NetpolResult) (severity, summary string) {
	return "pass", fmt.Sprintf("%d NetworkPolicy/ies matched", len(result.Policies))
}
