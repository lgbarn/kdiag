# Security Audit Report — Phase 5

## Executive Summary

**Verdict:** PASS
**Risk Level:** Medium

Phase 5 introduced no exploitable vulnerabilities and no hardcoded secrets. The refactoring is structurally sound: error propagation was improved throughout the ingress and VPC endpoint paths, and a shared concurrency-safe function replaces triplicated logic. Two issues warrant attention before this phase is considered fully clean. First, raw AWS API error strings from `ListNodeENIs` failures flow unsanitized into structured output (JSON and table), which may leak internal infrastructure details such as ARNs, request IDs, and instance IDs to end users — the same class of information that `sanitizeError` already protects against in the `diagnose` command. Second, `checkControllerHealth` in `cmd/ingress.go` conflates API errors with genuine absence of controller pods, a goal listed in PROJECT.md that was not addressed. No dependency changes were made, no secrets were introduced, and no injection or authentication issues exist.

### What to Do

| Priority | Finding | Location | Effort | Action |
|----------|---------|----------|--------|--------|
| 1 | Raw AWS error strings in SkippedNode.Reason reach user output unsanitized | `pkg/aws/eni.go:198`, `cmd/eks/eks.go:198` | Small | Apply `sanitizeError` (or equivalent) before storing the error string in `ENISkippedNode.Reason` |
| 2 | `checkControllerHealth` conflates API errors with absent pods | `cmd/ingress.go:220,226` | Small | Separate `if err != nil` from `len(pods.Items) == 0`; return distinct messages |
| 3 | `ClassifyIP` silently ignores `net.ParseCIDR` errors on hardcoded CIDRs | `pkg/aws/endpoint.go:56` | Trivial | Move CIDR parsing to package init; panic or log.Fatal on parse failure |
| 4 | Tests exercise `ComputeNodeUtilization` with `concurrency=0` (serial path) only | `pkg/aws/eni_test.go` | Small | Add a test with `concurrency > 1` and `t.Setenv("GORACE", "racy=1")` to catch races |

### Themes

- Error sanitization is inconsistently applied: `cmd/diagnose.go` correctly sanitizes errors before placing them in structured output, but the new `ENISkippedNode.Reason` field bypasses this protection and propagates raw SDK errors into both JSON and table output.
- The phase correctly fixes several silent-failure patterns (ingress API errors, VPC endpoint enrichment) but left the `checkControllerHealth` conflation fix incomplete despite it being an explicit goal in PROJECT.md.

---

## Detailed Findings

### Important

**[I1] Raw AWS SDK error strings stored in ENISkippedNode.Reason and rendered to user output**

- **Location:** `pkg/aws/eni.go:198` (stored), `cmd/eks/eks.go:198` (rendered), `cmd/eks/node.go:153-155` and `cmd/eks/cni.go:161-163` (propagated)
- **Description:** When `ListNodeENIs` fails for a node, the raw `err.Error()` string is stored verbatim in `ENISkippedNode.Reason`. That string is then propagated unchanged into `SkippedNode.Reason` in command-layer types and rendered to both stdout (table: `p.PrintRow(s.NodeName, s.Reason)`) and JSON output (`json:"skipped_nodes"`). AWS SDK error messages routinely include instance IDs, ARN paths, request IDs, and regional endpoint URLs — the same class of data that `sanitizeError` in `cmd/diagnose.go` was introduced specifically to strip. The concurrent goroutine path has the same behavior.

  The relevant code at `pkg/aws/eni.go:196-200`:
  ```go
  skipped = append(skipped, ENISkippedNode{
      NodeName: node.Name,
      Reason:   err.Error(),   // raw SDK error, no sanitization
  })
  ```

- **Impact:** An operator running `kdiag eks node --output json` or `kdiag eks cni` against a cluster with transient EC2 API failures will see raw AWS error messages including internal topology details in output that may be collected by logging pipelines, sent to ticketing systems, or displayed in dashboards. This is an information-disclosure issue (CWE-209: Generation of Error Message Containing Sensitive Information). It does not represent an exploitable vulnerability — only the CLI operator sees the output — but it violates the explicit design principle already established by `sanitizeError`.

- **Remediation:** Either (a) wrap the error before storing it — `Reason: sanitizeError(err.Error())` — or (b) define `sanitizeError` in `pkg/aws` and call it there. Option (b) is architecturally cleaner because it keeps the library self-contained. Alternatively, truncate to a fixed-length human-readable prefix without AWS request metadata.

---

**[I2] `checkControllerHealth` conflates API errors with absent controller pods — goal listed in PROJECT.md was not implemented**

- **Location:** `cmd/ingress.go:220-231`
- **Description:** The function collapses two distinct conditions into a single branch:

  ```go
  if err != nil || len(pods.Items) == 0 {
      // ...
      return "no controller pods found"
  }
  ```

  When the Kubernetes API returns an authorization or connectivity error (`err != nil`), the function returns the misleading string `"no controller pods found"` rather than indicating an API error. This can cause an operator to incorrectly conclude that no ingress controller exists, rather than that a permissions or network problem prevented the check. PROJECT.md goal 5 (`checkControllerHealth must distinguish API errors from genuine absence of controller pods`) was explicitly not addressed in this phase.

- **Impact:** False diagnosis: an RBAC misconfiguration or namespace restriction will be silently misreported as "controller not installed." This does not allow privilege escalation or data access, but it undermines diagnostic reliability and can delay incident response. (CWE-390: Detection of Error Condition Without Action)

- **Remediation:** Separate the two conditions:
  ```go
  if err != nil {
      return fmt.Sprintf("controller health check failed: %v", sanitizeError(err.Error()))
  }
  if len(pods.Items) == 0 {
      // ... fallback or return "no controller pods found"
  }
  ```

---

### Advisory

- **`ClassifyIP` silently ignores `net.ParseCIDR` parse errors on hardcoded CIDRs** (`pkg/aws/endpoint.go:56`). The expression `_, network, _ := net.ParseCIDR(cidr)` discards the error. Because these CIDRs are compile-time constants and all are well-formed, this will never fail in practice; however, the nil-network dereference on the next line (`network.Contains(ip)`) would panic at runtime if a malformed CIDR were ever introduced. Moving parsing to a package-level `var` block with a `panic` on parse failure is idiomatic Go and prevents silent bypass of IPv6 classification. (CWE-476 — nil dereference risk)

- **`ComputeNodeUtilization` tests use `concurrency=0` (coerced to serial) exclusively** (`pkg/aws/eni_test.go`). The concurrent goroutine pool path (`concurrency > 1`) is exercised in production callers with `concurrency=10` but not tested. The mutex and semaphore pattern is correct, but the race detector will never be applied to this path through the current test suite. Running tests with `-race -count=5` and a concurrency value greater than 1 would catch data-race regressions. (No immediate security impact, but test gap for a concurrency-sensitive function)

- **Verbose stderr output in `cmd/eks/node.go:153` and `cmd/eks/cni.go:161` prints raw `s.Reason`** (which carries the unsanitized AWS error string noted in I1) to stderr under `--verbose`. This is a narrower exposure than the structured output path but compounds finding I1.

- **`cmd/diagnose.go:174` passes `ingErr` raw to `fmt.Fprintf(os.Stderr, ...)` before the sanitized version is written to the structured report** (line 177). Both lines use the error, but the stderr line uses the raw error while the report line uses `sanitizeError`. The stderr path may leak URLs to operator-visible logs without redaction.

  ```go
  fmt.Fprintf(os.Stderr, "[kdiag] warning: ingress check failed: %v\n", ingErr)  // raw
  Summary: fmt.Sprintf("ingress API error: %v", sanitizeError(ingErr.Error())),   // sanitized
  ```

  Remediation: apply `sanitizeError` to the stderr Fprintf as well.

---

## Cross-Component Analysis

**Error sanitization boundary is inconsistently placed.** `cmd/diagnose.go` defines and applies `sanitizeError` before any error reaches structured or terminal output. This function is package-private to `cmd`, meaning library-layer code in `pkg/aws` cannot access it. The new `ENISkippedNode.Reason` field crosses the `pkg/aws` → `cmd` boundary carrying a raw error string, but the calling code in `cmd/eks/node.go` and `cmd/eks/cni.go` treats `s.Reason` as already-clean and passes it straight to output. The `cmd/diagnose.go` caller does the same (line 355). The existing pattern established at the `diagnose` command level — sanitize before output — was not carried forward to the new shared function's consumers. A cross-cutting policy (sanitize at the output layer, or sanitize before crossing a package boundary) should be documented and enforced.

**Non-deterministic output ordering from concurrent `ComputeNodeUtilization` is not flagged to callers.** The docstring notes "Result order is non-deterministic when concurrency > 1", and all three callers use `concurrency=10`. The `cmd/eks/node.go` caller then overlays per-node pod data via `eligibleByName[u.NodeName]` map lookup, which is order-independent. However, table output order will vary between invocations. If any caller relies on stable ordering for diffing outputs (e.g., in CI automation), this would produce spurious diffs. This is not a security issue but is a functional coherence point.

**`checkControllerHealth` error surface is isolated from the main diagnose error-handling architecture.** The diagnose command's `findIngressesForPod` was fixed to propagate errors, and those errors flow through `sanitizeError` before appearing in the report. But `checkControllerHealth` — called from `runIngress`, not from `findIngressesForPod` — still drops its errors silently. The two functions now have different error handling postures despite being in the same file.

---

## Analysis Coverage

| Area | Checked | Notes |
|------|---------|-------|
| Code Security (OWASP) | Yes | No injection, auth bypass, or deserialization issues found |
| Secrets & Credentials | Yes | No hardcoded secrets, API keys, or credentials in any changed file |
| Dependencies | Yes | No dependency changes in phase 5; go.sum committed |
| Infrastructure as Code | N/A | No IaC files changed |
| Docker/Container | N/A | No Dockerfiles changed |
| Configuration | Yes | No config files changed; `.shipyard/STATE.json` update contains no secrets |

---

## Dependency Status

No dependencies were added or modified in Phase 5. The existing dependency set was audited in prior phases. Key versions remain unchanged: `aws-sdk-go-v2 v1.41.3`, `k8s.io/client-go v0.32.3`, `github.com/spf13/cobra v1.9.1`.

| Package | Version | Known CVEs | Status |
|---------|---------|-----------|--------|
| All existing deps | (unchanged) | None identified against current versions | OK |

---

## IaC Findings

N/A — No infrastructure-as-code files were changed in Phase 5.
