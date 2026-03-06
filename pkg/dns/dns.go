// Package dns provides utilities for DNS diagnostics: FQDN construction,
// dig output parsing, command building, and CoreDNS pod health evaluation.
package dns

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// DNSResult holds the outcome of a DNS diagnostic query.
type DNSResult struct {
	Target      string       `json:"target"`
	FQDN        string       `json:"fqdn"`
	Resolved    []string     `json:"resolved"`
	QueryTimeMs int64        `json:"query_time_ms"`
	CoreDNS     []CoreDNSPod `json:"coredns_pods"`
	RawOutput   string       `json:"raw_output,omitempty"`
	Error       string       `json:"error,omitempty"`
}

// CoreDNSPod summarises the state of a single CoreDNS pod.
type CoreDNSPod struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
}

// BuildFQDN returns a fully-qualified domain name for the given service name
// and namespace. If the name already contains a dot it is returned unchanged;
// otherwise the standard in-cluster suffix is appended.
func BuildFQDN(name, namespace string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return name + "." + namespace + ".svc.cluster.local"
}

// ParseDigOutput parses the stdout of:
//
//	dig <target> @<server> +noall +answer +stats
//
// It extracts all A/AAAA answer IPs and the reported query time. An error is
// returned when the output is empty, or when the DNS status is a non-NOERROR
// status such as NXDOMAIN or SERVFAIL. A NOERROR response with an empty
// answer section (e.g. for a CNAME-only or negative cache hit) returns
// (nil, queryTimeMs, nil).
func ParseDigOutput(raw string) (resolved []string, queryTimeMs int64, err error) {
	if strings.TrimSpace(raw) == "" {
		return nil, 0, fmt.Errorf("dig output is empty")
	}

	var status string

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)

		// Answer lines: name TTL IN A|AAAA ip
		// Fields are tab-separated in real dig output; handle mixed whitespace.
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			recordType := strings.ToUpper(fields[3])
			if recordType == "A" || recordType == "AAAA" {
				resolved = append(resolved, fields[4])
			}
		}

		// Query time: extract from ";; Query time: N msec"
		if strings.Contains(line, "Query time:") {
			// e.g. "Query time: 3 msec"
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "time:" && i+1 < len(parts) {
					n, convErr := strconv.ParseInt(parts[i+1], 10, 64)
					if convErr == nil {
						queryTimeMs = n
					}
					break
				}
			}
		}

		// Status: extract from ";; ->>HEADER<<- opcode: QUERY, status: NXDOMAIN, ..."
		// Also handle "; Got answer:" style; just look for "status:"
		if strings.Contains(line, "status:") {
			parts := strings.Split(line, "status:")
			if len(parts) >= 2 {
				rest := strings.TrimSpace(parts[1])
				// status may be followed by comma, space, or other chars
				status = strings.FieldsFunc(rest, func(r rune) bool {
					return r == ',' || r == ' ' || r == '\t'
				})[0]
			}
		}
	}

	if len(resolved) == 0 {
		if status != "" && status != "NOERROR" {
			return nil, queryTimeMs, fmt.Errorf("DNS query returned status %s", status)
		}
		// NOERROR (or no status line found) with an empty answer section is
		// valid — return nil slice with no error.
		return nil, queryTimeMs, nil
	}

	return resolved, queryTimeMs, nil
}

// BuildDigCommand returns the argv slice for running dig. If dnsServerIP is
// non-empty the server is included as a positional "@ip" argument.
func BuildDigCommand(target, dnsServerIP string) []string {
	cmd := []string{"dig", target}
	if dnsServerIP != "" {
		cmd = append(cmd, "@"+dnsServerIP)
	}
	cmd = append(cmd, "+noall", "+answer", "+stats")
	return cmd
}

// EvaluateCoreDNSPods converts a list of CoreDNS pods into CoreDNSPod
// summaries. A pod is considered Ready only when every container reports
// Ready == true.
func EvaluateCoreDNSPods(pods []corev1.Pod) []CoreDNSPod {
	result := make([]CoreDNSPod, 0, len(pods))
	for _, pod := range pods {
		ready := len(pod.Status.ContainerStatuses) > 0
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				ready = false
				break
			}
		}
		result = append(result, CoreDNSPod{
			Name:   pod.Name,
			Status: string(pod.Status.Phase),
			Ready:  ready,
		})
	}
	return result
}
