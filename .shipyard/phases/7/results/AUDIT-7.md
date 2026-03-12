# Security Audit Report — Phase 7

## Executive Summary

**Verdict:** PASS
**Risk Level:** Low

Phase 7 delivers three targeted bug fixes: a pre-flight RBAC check reuse in `cmd/shell.go`, an input guard for the `--status` flag in `cmd/eks/node.go`, and proper error handling for a discarded `WriteString` return value. None of the changes introduce new attack surface, and no exploitable vulnerabilities were found. Two advisory-level findings are noted: the `--status` flag accepts arbitrary strings without enum validation (producing silent empty output rather than an error), and the pre-flight RBAC result reused in the forbidden-error path may be stale in a narrow race window — resulting in a degraded error message only, with no security consequence. Fix the enum validation first; it is the most user-facing of the two.

### What to Do

| Priority | Finding | Location | Effort | Action |
|----------|---------|----------|--------|--------|
| 1 | `--status` flag accepts arbitrary strings silently | `cmd/eks/node.go:190-198` | Trivial | Validate against `{"EXHAUSTED","WARNING","OK"}` set at line 94 |
| 2 | Pre-flight RBAC result may be stale at forbidden-error site | `cmd/shell.go:123` | Small | Document the TOCTOU bound or add a comment; no code change strictly required |

### Themes

- Input validation is applied earlier in the call stack (flag presence check) but not for value correctness — a consistent gap in CLI defensive hygiene.
- RBAC error reporting improved correctly; the simplification is sound and eliminates a redundant API call that was silently swallowing errors.

---

## Detailed Findings

### Critical

_None._

### Important

_None._

---

### Advisory

**[A1] `--status` flag accepts arbitrary strings without enum validation**
- **Location:** `cmd/eks/node.go:190-198`
- **Description:** The guard added at line 94 correctly enforces that `--status` requires `--show-pods`, but does not validate that the value is one of the three documented enum values (`EXHAUSTED`, `WARNING`, `OK`). The value is uppercased at line 191 and then compared against the internal `Status` field. An unrecognized value — such as `--status CRITICAL` or `--status garbage` — silently produces an empty node list rather than an error, misleading users into thinking no nodes exist matching the filter.
- **Impact:** Not a security vulnerability. No injection is possible because the user-supplied string is only used as a string equality comparator against data already held in memory — it is never interpolated into a query, shell command, or external call. The risk is UX confusion and potential silent data loss during scripted use.
- **Remediation:** Add an enum check immediately after the existing guard at line 94:
  ```go
  validStatuses := map[string]bool{"EXHAUSTED": true, "WARNING": true, "OK": true}
  if statusFilter != "" && !validStatuses[strings.ToUpper(statusFilter)] {
      return fmt.Errorf("--status %q is not valid; must be one of: EXHAUSTED, WARNING, OK", statusFilter)
  }
  ```
- **Standards reference:** CWE-20 (Improper Input Validation)

---

**[A2] Pre-flight RBAC result reused at forbidden-error site — potential TOCTOU in message accuracy**
- **Location:** `cmd/shell.go:97` (pre-flight check), `cmd/shell.go:123` (reuse at error site)
- **Description:** The Phase 7 fix removes a duplicate `CheckEphemeralContainerRBAC` call (the former `checks2`) and reuses the pre-flight `checks` result when `CreateEphemeralContainer` returns a `403 Forbidden` error. This is a correct simplification: if the pre-flight RBAC check passed (line 101), the `FormatRBACError(checks)` call at line 123 will return an empty string, and the code falls through to the generic forbidden message at line 127 — which is accurate behavior.

  The narrow concern is a TOCTOU window: if RBAC permissions are **revoked** between the pre-flight check (line 97) and the `CreateEphemeralContainer` call (line 113), the message will say "check your RBAC permissions" without listing which permissions are missing. The previous code addressed this by re-fetching checks at error time, but silently discarded that error (`rbacCheckErr`), meaning the re-fetch itself could fail undetected. The current approach is strictly safer on the error-handling axis; it only loses diagnostic detail in a narrow race.
- **Impact:** In the TOCTOU race, the user receives a slightly less specific error message. There is no security impact — the operation is correctly rejected by the Kubernetes API. No information is leaked; no bypass is possible.
- **Remediation:** This is informational. If maximally precise error messages are desired under RBAC revocation, the caller can attempt a fresh `CheckEphemeralContainerRBAC` call inside the `IsForbidden` branch (ensuring its own error is handled). A code comment documenting the current behavior and the accepted trade-off would suffice.
- **Standards reference:** CWE-362 (Race Condition / TOCTOU) — informational; no exploitability

---

## Cross-Component Analysis

**RBAC check coherence across `shell.go` paths.** Both `runPodShell` and `runNodeShell` perform RBAC pre-flight checks before attempting privileged operations. The pod shell path uses `CheckEphemeralContainerRBAC` (three permission checks); the node shell path uses two independent `CheckSingleRBAC` calls. Both paths correctly fail fast on missing permissions before attempting API mutations. The Phase 7 fix brings the `runPodShell` error path into alignment with this pattern — it no longer silently discards the error from a second RBAC check.

**Error propagation consistency.** The `WriteString` fix in `cmd/eks/node.go:275` closes a gap where an I/O error at the summary write step was silently discarded. This is consistent with how all other write operations in the file already propagate errors (e.g., `p.Flush()` at line 227, `printSkippedNodes` at line 263). The phase now has uniform error propagation across the output path.

**Input validation boundary.** The `--status` flag guard (node.go:94) is the first input validation applied after flag parsing. This is the correct location for a presence-dependency check. However, value validation (`EXHAUSTED|WARNING|OK`) is missing at this boundary while being implicitly assumed downstream at line 194. The trust boundary should be closed at the guard, not left open to silent no-op behavior deep in the output path.

**No cross-component data leakage.** Status filter values are not written to logs, error messages, or any external call. The RBAC error messages produced by `FormatRBACError` contain only permission names (fixed strings from the Kubernetes API spec), not user-supplied input — no injection or reflected-content risk.

---

## Analysis Coverage

| Area | Checked | Notes |
|------|---------|-------|
| Code Security (OWASP) | Yes | Both changed files reviewed in full; no injection, auth bypass, or data exposure found |
| Secrets & Credentials | Yes | All changed files and test fixtures scanned; no hardcoded credentials found |
| Dependencies | Yes | No dependency changes in Phase 7; go.mod and go.sum unchanged |
| Infrastructure as Code | N/A | No IaC files changed |
| Docker/Container | N/A | No Dockerfile or container config changes |
| Configuration | Yes | No configuration files changed; flag defaults reviewed in root.go |

---

## Dependency Status

No dependencies were added or modified in Phase 7. No dependency audit action required.

| Package | Version | Known CVEs | Status |
|---------|---------|-----------|--------|
| (no changes) | — | — | OK |

---

## IaC Findings

Not applicable — no infrastructure-as-code files were modified in Phase 7.
