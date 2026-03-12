---
phase: phase-5
plan: "1.2"
wave: 1
dependencies: []
must_haves:
  - EnrichWithVpcEndpoints returns ([]EndpointCheckResult, error) — error is the unwrapped DescribeVpcEndpoints error
  - cmd/eks/endpoint.go call site checks the returned error; on error sets apiEnriched=false and prints a warning to stderr
  - ClassifyIP recognizes fc00::/7 (IPv6 ULA), ::1/128 (IPv6 loopback), 127.0.0.0/8 (IPv4 loopback), 169.254.0.0/16 (IPv4 link-local), fe80::/10 (IPv6 link-local) as "private"
  - findIngressesForPod returns ([]IngressRuleResult, []IngressTLSResult, error) — both API call errors propagate
  - cmd/diagnose.go ingress check: on non-nil error prints warning to stderr, appends DiagnoseCheckResult with Severity=SeverityWarn
  - Tests added for all new ClassifyIP ranges (TDD)
  - Tests added for EnrichWithVpcEndpoints error path (TDD)
files_touched:
  - pkg/aws/endpoint.go
  - pkg/aws/endpoint_test.go
  - cmd/eks/endpoint.go
  - cmd/ingress.go
  - cmd/diagnose.go
tdd: true
---

## Context

This plan fixes three independent correctness bugs that do not touch the ENI utilization
logic at all. It can execute in parallel with Plan 1.1 because their file sets are disjoint.

### Bug A — EnrichWithVpcEndpoints swallows DescribeVpcEndpoints error

Current signature in `pkg/aws/endpoint.go:77`:
```go
func EnrichWithVpcEndpoints(...) []EndpointCheckResult
```
Error dropped at lines 100-102. Call site in `cmd/eks/endpoint.go:76` sets
`apiEnriched = true` unconditionally, producing misleading JSON output when the AWS call
actually failed.

Fix: change signature to `([]EndpointCheckResult, error)`. Wrap the error with
`fmt.Errorf("DescribeVpcEndpoints: %w", err)`. Call site: check the returned error; on
error set `apiEnriched = false` and, if `isVerbose()`, print
`[kdiag] warning: VPC endpoint enrichment failed: <err>` to stderr. Do not return the
error from `runEndpoint` — degrade gracefully, matching the existing `ec2Err` path.

### Bug B — ClassifyIP misclassifies IPv6 and loopback addresses as "public"

Current implementation in `pkg/aws/endpoint.go:43-51` checks only RFC 1918 ranges.

Add the following CIDRs to the loop (order does not matter; all are checked before returning
"public"):
- `"127.0.0.0/8"` — IPv4 loopback
- `"169.254.0.0/16"` — IPv4 link-local (includes 169.254.169.254 AWS metadata endpoint)
- `"::1/128"` — IPv6 loopback
- `"fc00::/7"` — IPv6 ULA (covers fd00::/8 used by VPC-assigned addresses)
- `"fe80::/10"` — IPv6 link-local

No type changes required. `net.ParseCIDR` handles IPv6 natively. The existing loop structure
is correct; only the CIDR string list needs expanding.

Update the Godoc comment on `ClassifyIP` to reflect the expanded set.

### Bug C — findIngressesForPod returns nil on API errors, masking failures in diagnose

Current signature in `cmd/ingress.go:249`:
```go
func findIngressesForPod(...) ([]IngressRuleResult, []IngressTLSResult)
```
Two error paths return `(nil, nil)` — lines 252-254 (Services.List fails) and 278-281
(Ingresses.List fails). The diagnose call site at `cmd/diagnose.go:172` interprets `(nil, nil)`
as "no ingress references found" and emits SeverityPass.

Fix: change return signature to `([]IngressRuleResult, []IngressTLSResult, error)`.
- Services.List failure: return `nil, nil, fmt.Errorf("list services: %w", err)`
- Ingresses.List failure: return `nil, nil, fmt.Errorf("list ingresses: %w", err)`
- Happy-path callers of findIngressesForPod remain unchanged (just add `_` or handle error).

Update `cmd/ingress.go` line 241 call site (`runIngress` does not call `findIngressesForPod`
directly — only `runDiagnose` does). Actually, `findIngressesForPod` is only called from
`cmd/diagnose.go:172`. Update that call site to:
```go
ingRules, ingTLS, ingErr := findIngressesForPod(ctx, client, namespace, pod)
if ingErr != nil {
    fmt.Fprintf(os.Stderr, "[kdiag] warning: ingress check failed: %v\n", ingErr)
    report.Checks = append(report.Checks, DiagnoseCheckResult{
        Name: "ingress", Severity: SeverityWarn,
        Summary: fmt.Sprintf("ingress API error: %v", sanitizeError(ingErr.Error())),
    })
} else {
    sev, sum := ingressSeverity(ingRules, ingTLS)
    report.Checks = append(report.Checks, DiagnoseCheckResult{
        Name: "ingress", Severity: sev, Summary: sum,
    })
}
```
The `IsVerbose()` / `isVerbose()` guard is omitted for the warning message here intentionally:
the CONTEXT-5.md decision says "print a warning line to stderr" on error — this should always
be visible, not gated on verbose mode, because it signals a degraded check result.

Note: `checkControllerHealth` conflation of API error with "no pods" is NOT in scope for
this plan. The RESEARCH.md marks it lower priority (only affects standalone `kdiag ingress`,
not `diagnose`). Leave it for a future phase.

---

```xml
<task id="1" files="pkg/aws/endpoint_test.go" tdd="true">
  <action>
    Add test cases to pkg/aws/endpoint_test.go following the existing banner style.

    Section 1 — extend TestClassifyIP_Private with IPv6 and loopback cases. Add a new
    table within the existing test or as a second test function TestClassifyIP_Extended
    covering:
      - "127.0.0.1" → "private"
      - "127.255.255.255" → "private"
      - "169.254.169.254" → "private"  (AWS metadata endpoint)
      - "169.254.0.1" → "private"
      - "::1" → "private"
      - "fd00::1" → "private"          (VPC ULA — within fc00::/7)
      - "fc00::1" → "private"          (fc00::/7 boundary)
      - "fe80::1" → "private"
      - "2001:db8::1" → "public"       (documentation range, not private)
      - "8.8.8.8" → "public"           (existing public case for regression)

    Section 2 — add TestEnrichWithVpcEndpoints_Error testing the new error return.
    Use mockEC2API with describeVpcEndpoints returning a sentinel error. Assert that
    EnrichWithVpcEndpoints returns the same results slice unchanged AND returns a non-nil
    error wrapping the sentinel (use errors.Is).
  </action>
  <verify>cd /Users/lgbarn/Personal/kdiag && go test ./pkg/aws/... 2>&1 | grep -E "FAIL|does not compile" | head -5</verify>
  <done>Tests compile (may fail until Task 2 adds implementation). The new test cases exist
  in the file. Existing TestClassifyIP_Private and TestBuildServiceEndpoints pass unchanged.</done>
</task>

<task id="2" files="pkg/aws/endpoint.go, cmd/eks/endpoint.go, cmd/ingress.go, cmd/diagnose.go" tdd="true">
  <action>
    Apply three coordinated fixes:

    Fix A — pkg/aws/endpoint.go:
    1. Change EnrichWithVpcEndpoints signature to return ([]EndpointCheckResult, error).
    2. Replace the `return results` at the error path with
       `return results, fmt.Errorf("DescribeVpcEndpoints: %w", err)`.
    3. Change the happy-path return at end of function to `return results, nil`.
    4. Update Godoc to document the error return.

    Fix A call site — cmd/eks/endpoint.go:
    1. Change line 76 to `results, enrichErr := awspkg.EnrichWithVpcEndpoints(...)`.
    2. After the call: if enrichErr != nil, set apiEnriched = false and print
       `fmt.Fprintf(os.Stderr, "[kdiag] warning: VPC endpoint enrichment failed: %v\n", enrichErr)`
       (no isVerbose guard — this is a degraded result the user must see).
    3. Remove the unconditional `apiEnriched = true` and set it only on enrichErr == nil.

    Fix B — pkg/aws/endpoint.go:
    1. Expand the CIDR slice in ClassifyIP to include:
       "127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10"
       appended after the three existing RFC 1918 entries.
    2. Update the Godoc comment from "RFC 1918 addresses" to
       "RFC 1918, IPv4 loopback, IPv4 link-local, IPv6 ULA (fc00::/7),
        IPv6 loopback, and IPv6 link-local addresses".

    Fix C — cmd/ingress.go:
    1. Change findIngressesForPod signature to
       ([]IngressRuleResult, []IngressTLSResult, error).
    2. Services.List error path: return nil, nil, fmt.Errorf("list services: %w", err).
    3. Ingresses.List error path: return nil, nil, fmt.Errorf("list ingresses: %w", err).
    4. Happy-path return: return rules, tlsResults, nil.

    Fix C call site — cmd/diagnose.go:
    1. Update line 172 to capture three return values.
    2. On non-nil ingErr: print warning to stderr (no verbose gate), append
       DiagnoseCheckResult{Name: "ingress", Severity: SeverityWarn,
       Summary: fmt.Sprintf("ingress API error: %v", sanitizeError(ingErr.Error()))}.
    3. On nil ingErr: existing ingressSeverity path unchanged.
  </action>
  <verify>cd /Users/lgbarn/Personal/kdiag && go build ./... && go test ./pkg/aws/... ./cmd/...</verify>
  <done>go build ./... exits 0 (all packages compile). go test ./pkg/aws/... passes all
  TestClassifyIP_* and TestEnrichWithVpcEndpoints_* cases including the new ones.
  go test ./cmd/... exits 0. Confirm with:
    go vet ./pkg/aws/... ./cmd/eks/... ./cmd/
  Zero vet warnings.</done>
</task>
```
