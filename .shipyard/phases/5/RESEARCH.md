# Research: Phase 5 — ENI Deduplication, Error Handling, IPv6 ClassifyIP

## Context

kdiag is a Go 1.25 CLI (cobra framework) for Kubernetes diagnostics. Phase 5 targets three
distinct but related problems discovered during Phase 4 work:

1. **ENI utilization logic is duplicated across three call sites** with subtle behavioral
   differences, making it hard to keep in sync and impossible to test once.
2. **`EnrichWithVpcEndpoints` silently swallows its AWS error**, leaving callers unable to
   distinguish "no VPC endpoints exist" from "the API call failed".
3. **`findIngressesForPod` and `checkControllerHealth` return nil on API errors** without
   surfacing any signal to the diagnose orchestrator.
4. **`ClassifyIP` handles only RFC 1918 IPv4 ranges**, so IPv6 loopback and ULA addresses
   are misclassified as "public".

The existing CONTEXT-5.md documents the architectural decisions already agreed for this phase.
This document provides the precise code-level evidence the architect needs to plan changes.

---

## Section 1 — ENI Utilization Logic (Three Locations)

### 1.1 `cmd/eks/node.go` — `runNode`

**File:** `cmd/eks/node.go`, lines 138–196

**Data sources:**
- `awspkg.ListNodeENIs(ctx, ec2Client, en.InstanceID)` → `*NodeENIInfo`
  - `eniInfo.ENIs` — `[]ENISummary`, used as `len(eniInfo.ENIs)` for current ENI count
  - `eniInfo.TotalIPs` — `int`, sum of `len(eni.PrivateIpAddresses)` across all ENIs
- `awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)` → `map[string]*InstanceLimits`
  - `limits.MaxENIs` — `int32`
  - `limits.MaxIPsPerENI` — `int32`

**Calculation (lines 161–178):**
```go
maxTotalIPs := int(maxENIs) * int(maxIPsPerENI)          // no prefix-delegation multiplier
utilPct := (currentIPs * 100) / maxTotalIPs
status := "OK"
if utilPct >= 85 { status = "EXHAUSTED" }
else if utilPct >= 70 { status = "WARNING" }
```

**Thresholds:** EXHAUSTED at >= 85%, WARNING at >= 70%. Both statuses are tracked.

**Output type:** `NodeENIStatus` (line 28), a per-node struct included in `NodeReport.Nodes`.
`ExhaustedNodes` in `NodeSummaryInfo` is incremented only for EXHAUSTED, not WARNING.

**Error behavior:** On `ListNodeENIs` failure the node is appended to `report.Skipped` and the
loop continues (line 146–151). On missing limits the node is also skipped (lines 154–159).

---

### 1.2 `cmd/eks/cni.go` — `runCNI`

**File:** `cmd/eks/cni.go`, lines 156–209

**Data sources:** Same `ListNodeENIs` and `GetInstanceTypeLimits` calls.

**Calculation (lines 170–196):**
```go
if prefixDelegation {
    maxTotalIPs = int(maxENIs) * int(maxIPsPerENI) * 16   // prefix-delegation multiplier
} else {
    maxTotalIPs = int(maxENIs) * int(maxIPsPerENI)
}
utilPct := (currentIPs * 100) / maxTotalIPs
exhausted := utilPct >= 85                                 // boolean only — no WARNING level
```

**Key difference from `node.go`:** Applies a `* 16` multiplier when
`cniConfig.PrefixDelegation` is true. The WARNING threshold (70%) does **not** exist here;
the field `NodeCapacity.Exhausted` is a plain `bool`. The table display (line 273–274) renders
EXHAUSTED or OK only — no WARNING column.

**Output type:** `NodeCapacity` (line 46), collected into `CNIReport.Nodes`. Exhausted node
names accumulate in `CNIReport.IPExhausted []string`.

**Error behavior:** Same skip-and-continue pattern as `node.go` (lines 158–167).

---

### 1.3 `cmd/diagnose.go` — `countExhaustedNodes`

**File:** `cmd/diagnose.go`, lines 327–366

**Signature:**
```go
func countExhaustedNodes(ctx context.Context, ec2Client awspkg.EC2API, nodes []corev1.Node) (int, error)
```

**Calculation (lines 347–364):**
```go
maxTotalIPs := int(limits.MaxENIs) * int(limits.MaxIPsPerENI)  // no prefix-delegation
utilPct := (eniInfo.TotalIPs * 100) / maxTotalIPs
if utilPct >= 85 { exhausted++ }
```

**Key difference from both others:** Returns only a count (`int`), not per-node details.
No WARNING threshold. No prefix-delegation multiplier. Errors from `ListNodeENIs` are
silently skipped (`continue` on line 350) — a stale node error is treated as "not exhausted",
potentially under-counting.

**Called from:** `runDiagnose` (line 210). The caller wraps errors from
`GetInstanceTypeLimits` but not per-node ENI query errors, which are already swallowed inside
`countExhaustedNodes`.

---

### 1.4 Supporting Types in `pkg/aws/eni.go`

**File:** `pkg/aws/eni.go`

**`ListNodeENIs` signature (line 36):**
```go
func ListNodeENIs(ctx context.Context, api EC2API, instanceID string) (*NodeENIInfo, error)
```
Returns `*NodeENIInfo`:
- `InstanceID string`
- `ENIs []ENISummary` — each with `ENIID`, `DeviceIndex`, `Description`, `PrivateIPCount`, `SecurityGroups`
- `TotalIPs int`

Note: `TotalIPs` counts `len(eni.PrivateIpAddresses)` which includes primary and secondary
private IPv4 addresses. IPv6 addresses on ENIs are **not** counted here.

**`GetInstanceTypeLimits` signature (line 79):**
```go
func GetInstanceTypeLimits(ctx context.Context, api EC2API, instanceTypes []string) (map[string]*InstanceLimits, error)
```
Returns `map[string]*InstanceLimits` where each entry has `MaxENIs int32`, `MaxIPsPerENI int32`.
Reads `NetworkInfo.Ipv4AddressesPerInterface` — again IPv4-only.

---

### 1.5 Duplication Summary

| Dimension | `node.go` | `cni.go` | `diagnose.go` |
|---|---|---|---|
| Prefix-delegation multiplier | No | Yes (x16) | No |
| WARNING threshold (70%) | Yes | No | No |
| EXHAUSTED threshold (85%) | Yes | Yes | Yes |
| Output granularity | Per-node struct | Per-node struct | Count only |
| ENI query error handling | Skip to Skipped list | Skip to Skipped list | Silent `continue` |
| Called by | `runNode` | `runCNI` | `runDiagnose` |

The CONTEXT-5.md decision is to extract a shared `pkg/aws` function returning
`[]NodeUtilization` with per-node data. Each caller then projects what it needs.
The shared function must accept a `prefixDelegation bool` parameter to keep the x16 behavior
for `cni.go` while remaining correct for the other two callers.

---

## Section 2 — `EnrichWithVpcEndpoints` Error Swallow

### 2.1 Current Function Signature

**File:** `pkg/aws/endpoint.go`, lines 77–114

```go
func EnrichWithVpcEndpoints(
    ctx context.Context,
    api EC2API,
    region string,
    results []EndpointCheckResult,
) []EndpointCheckResult
```

The function calls `api.DescribeVpcEndpoints` at line 95. On error (line 100–102):
```go
if err != nil {
    return results   // error silently discarded
}
```

The enriched fields (`EndpointID`, `EndpointType`, `State`) remain zero-valued on all
results. The caller cannot tell whether enrichment was attempted and failed or simply did
not apply.

### 2.2 Call Site

**File:** `cmd/eks/endpoint.go`, lines 73–78

```go
ec2Client, ec2Err := newEC2Client(ctx, k8sClient.Config.Host)
if ec2Err == nil {
    results = awspkg.EnrichWithVpcEndpoints(ctx, ec2Client, region, results)
    apiEnriched = true
}
```

`apiEnriched` is set to `true` unconditionally when `ec2Err == nil`, even if
`EnrichWithVpcEndpoints` internally failed. The `EndpointReport.APIEnriched` field is then
output as `true` in JSON, which is misleading: the user sees a table with empty ENDPOINT_TYPE
columns and `api_enriched: true`.

**Agreed change (CONTEXT-5.md):** Change signature to:
```go
func EnrichWithVpcEndpoints(
    ctx context.Context,
    api EC2API,
    region string,
    results []EndpointCheckResult,
) ([]EndpointCheckResult, error)
```

The call site in `cmd/eks/endpoint.go` must be updated to check the returned error. The
decision on how to surface it to the user (log a warning, set `apiEnriched = false`, or
return the error) should be made by the architect.

---

## Section 3 — `findIngressesForPod` and `checkControllerHealth` Error Handling

### 3.1 `findIngressesForPod`

**File:** `cmd/ingress.go`, lines 249–354

**Signature:**
```go
func findIngressesForPod(
    ctx context.Context,
    client *k8s.Client,
    namespace string,
    pod *corev1.Pod,
) ([]IngressRuleResult, []IngressTLSResult)
```

**Error behavior:**
- Line 252–254: `Services().List()` fails → returns `nil, nil`
- Line 278–281: `Ingresses().List()` fails → returns `nil, nil`

Both paths return `(nil, nil)` with no error propagation to the caller.

### 3.2 Call Site in `cmd/diagnose.go`

**File:** `cmd/diagnose.go`, lines 171–177

```go
if pod != nil {
    ingRules, ingTLS := findIngressesForPod(ctx, client, namespace, pod)
    sev, sum := ingressSeverity(ingRules, ingTLS)
    report.Checks = append(report.Checks, DiagnoseCheckResult{
        Name: "ingress", Severity: sev, Summary: sum,
    })
}
```

When the Services or Ingresses list call fails, the caller receives `(nil, nil)`. `ingressSeverity`
on empty inputs returns a pass-like or info result — the diagnose check silently shows no ingress
found rather than signaling an API failure.

**Agreed change (CONTEXT-5.md):** Change the function to signal API errors. The architect
must decide whether to change the return signature to include an error or use an internal
sentinel. The CONTEXT-5.md decision is to surface as WARN with a stderr message rather than
FAIL, so the diagnose run completes.

### 3.3 `checkControllerHealth`

**File:** `cmd/ingress.go`, lines 203–246

**Signature:**
```go
func checkControllerHealth(
    ctx context.Context,
    client *k8s.Client,
    controller string,
) string
```

**Error behavior:**
- Line 220: `Pods().List()` fails → `err != nil` evaluates to the same branch as "no pods
  found" (`len(pods.Items) == 0`). Returns `"no controller pods found"` even when the error
  is a transient API failure rather than genuine absence.
- Line 226: nginx fallback `Pods().List()` fails → same conflation.

The function returns a string. There is no way to distinguish "could not reach API" from
"there are genuinely no controller pods" — both return the same string.

**Call sites:**
- `cmd/ingress.go` line 178: `ctrlHealth := checkControllerHealth(ctx, client, controller)`
  Result is placed in `IngressResult.CtrlHealth string` — the caller does not try to act on
  failure.
- `cmd/diagnose.go` does **not** call `checkControllerHealth` directly; it calls
  `findIngressesForPod` which does not invoke controller health either. Controller health is
  only used in the standalone `kdiag ingress` command.

The architect should decide whether to change `checkControllerHealth` to return `(string, error)`
or keep it as a best-effort string and surface the error inside the string itself (e.g.,
`"error: <msg>"`). Given that it is only called from the standalone ingress command, the
impact is lower priority than `findIngressesForPod`.

---

## Section 4 — `ClassifyIP` and IPv6

### 4.1 Current Implementation

**File:** `pkg/aws/endpoint.go`, lines 43–51

```go
func ClassifyIP(ip net.IP) string {
    for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
        _, network, _ := net.ParseCIDR(cidr)
        if network.Contains(ip) {
            return "private"
        }
    }
    return "public"
}
```

Ranges checked: RFC 1918 only (10/8, 172.16/12, 192.168/16).

**Absent ranges:**
- `::1/128` — IPv6 loopback
- `fc00::/7` — IPv6 Unique Local Address (ULA), the IPv6 analog of RFC 1918
- `127.0.0.0/8` — IPv4 loopback (would also be misclassified as "public")
- `169.254.0.0/16` — IPv4 link-local (AWS instance metadata endpoint)
- `fe80::/10` — IPv6 link-local

**Impact:** If an AWS service hostname resolves to an IPv6 address (e.g., dual-stack VPC
endpoint, EKS cluster endpoint), `ClassifyIP` returns `"public"` even when the address is
within the ULA range (`fd00::/8` is the common VPC-assigned ULA sub-range). This causes
`kdiag eks endpoint` to report private VPC endpoints as "public".

### 4.2 Existing Test Coverage

**File:** `pkg/aws/endpoint_test.go`, lines 8–28

```go
func TestClassifyIP_Private(t *testing.T) {
    tests := []struct{ ip, want string }{
        {"10.0.1.5",       "private"},
        {"172.16.0.1",     "private"},
        {"172.31.255.255", "private"},
        {"192.168.1.1",    "private"},
        {"54.239.28.85",   "public"},
        {"3.5.140.2",      "public"},
    }
    ...
}
```

No IPv6 test cases exist. No loopback test cases exist. Adding test cases for
`::1`, `fd00::1`, `fe80::1`, `127.0.0.1`, and `169.254.169.254` should precede any
implementation change.

### 4.3 `net.IP` Behavior Note

`net.ParseIP("::1")` returns a 16-byte `net.IP`. `net.ParseCIDR` also returns a `*net.IPNet`
that handles both 4-byte and 16-byte representations correctly via `Contains`. Adding IPv6
CIDRs to the loop in `ClassifyIP` requires no type changes — only additional CIDR strings.

---

## Section 5 — Conventions to Follow

**File:** `.shipyard/codebase/CONVENTIONS.md`

Key points applicable to Phase 5 changes:

- **Error wrapping:** `fmt.Errorf("description: %w", err)` — always wrap with context.
  Never use bare `err` as the return without wrapping.
- **Error propagation in loops:** The pattern for per-item failures is `continue` (skip the
  node and log to stderr if verbose). This is already followed in `node.go` and `cni.go`;
  `countExhaustedNodes` silently skips without even a verbose log — a deviation to fix.
- **Verbose stderr prefix:** `[kdiag]` prefix, guarded by `isVerbose()`. Any new warning
  paths should follow this pattern.
- **Import ordering:** stdlib / third-party (k8s, cobra, aws-sdk) / internal (`github.com/lgbarn/kdiag/...`).
- **Godoc on all exported symbols:** Any new exported function or type in `pkg/aws/eni.go`
  needs a doc comment.
- **Compile-time interface assertion:** If `EC2API` is extended, the assertion
  `var _ EC2API = (*ec2.Client)(nil)` in `pkg/aws/ec2iface.go` line 20 catches drift.
- **JSON struct tags:** snake_case, `omitempty` for optional fields.
- **No named return values.**
- **`make` with capacity hints:** `make([]T, 0, len(src))` when the capacity is known.
- **Test section separators:** `// -------- TestFunctionName` banner style in `*_test.go`.

---

## Uncertainty Flags

- **Prefix-delegation in shared function:** CONTEXT-5.md says the shared function should
  accept `prefixDelegation bool`. However `countExhaustedNodes` (diagnose) does not currently
  apply prefix delegation, and there is no `aws-node` DaemonSet query in the diagnose path.
  The architect must decide whether `countExhaustedNodes` should gain prefix-delegation
  awareness or remain IPv4-only and accept the count difference.

- **`checkControllerHealth` return type change scope:** Whether to change
  `checkControllerHealth` to `(string, error)` affects `cmd/ingress.go` callers. Since it is
  not called from `runDiagnose`, this may be out of Phase 5 scope. The CONTEXT-5.md does not
  explicitly address it.

- **IPv4 loopback and link-local classification:** CONTEXT-5.md specifies "IPv6 ClassifyIP"
  but does not mention whether IPv4 loopback (`127.0.0.0/8`) and link-local (`169.254.0.0/16`)
  should also be added. These are reachable from the host and not routable public IPs.
  Clarification is needed on intended scope.

- **`EnrichWithVpcEndpoints` error user-facing behavior:** CONTEXT-5.md says "return error
  to caller" but does not specify whether the endpoint command should surface this as a
  warning, an error exit, or simply set `api_enriched: false` and continue. The call site
  already has an `ec2Err` path that degrades gracefully; the new error from
  `EnrichWithVpcEndpoints` could follow the same degradation pattern.

---

## Sources

All findings are from direct code inspection. No external URLs were consulted for this
research document. File paths are relative to the repository root
`/Users/lgbarn/Personal/kdiag/`.

1. `cmd/eks/node.go` — `runNode` function, lines 138–298
2. `cmd/eks/cni.go` — `runCNI` function, lines 72–300
3. `cmd/diagnose.go` — `countExhaustedNodes` function, lines 327–366; `runDiagnose` ingress
   call, lines 171–177
4. `pkg/aws/eni.go` — `ListNodeENIs` and `GetInstanceTypeLimits`, lines 36–114
5. `pkg/aws/endpoint.go` — `ClassifyIP` (lines 43–51), `EnrichWithVpcEndpoints` (lines 77–114)
6. `cmd/eks/endpoint.go` — `runEndpoint`, lines 36–96
7. `cmd/ingress.go` — `findIngressesForPod` (lines 249–354), `checkControllerHealth`
   (lines 203–246)
8. `pkg/aws/ec2iface.go` — `EC2API` interface, lines 11–20
9. `pkg/aws/endpoint_test.go` — `TestClassifyIP_Private`, lines 8–28
10. `.shipyard/phases/5/CONTEXT-5.md` — architectural decisions for Phase 5
11. `.shipyard/codebase/CONVENTIONS.md` — coding conventions
