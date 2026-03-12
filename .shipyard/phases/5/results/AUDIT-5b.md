# Security Audit Report — Phase 5 (Full Codebase Review)

## Executive Summary

**Verdict:** PASS
**Risk Level:** Medium

This is a diagnostics CLI used by cluster operators — it has no server-side attack surface, no user-facing web endpoints, and no persistent secret storage. The most significant finding is that the `diagnose` command propagates raw Kubernetes and AWS API error messages directly into its JSON output's `error` field, which can leak cluster endpoint hostnames, internal IP addresses, and AWS resource IDs to any system that ingests those JSON reports. A second design concern is that the `dns` command always includes the full raw output of `dig` in JSON mode via the `raw_output` field — this is structural over-disclosure rather than a bug, but warrants a deliberate decision. There are no exploitable vulnerabilities, no committed secrets, and no injection risks. Fix the structured error leakage in `diagnose` before shipping JSON output to log aggregators.

### What to Do

| Priority | Finding | Location | Effort | Action |
|----------|---------|----------|--------|--------|
| 1 | Raw API errors in diagnose JSON output | `cmd/diagnose.go:52,68,83,124` | Small | Replace `err.Error()` with sanitized summary strings in `DiagnoseCheckResult.Error` |
| 2 | `dig` raw output always included in JSON | `pkg/dns/dns.go:20`, `cmd/dns.go:159` | Trivial | Change `raw_output` tag to `json:"raw_output,omitempty"` and only populate it when `--verbose` is active |
| 3 | `gorilla/websocket` outdated (v1.5.0 → v1.5.3) | `go.mod:49` | Trivial | Run `go get github.com/gorilla/websocket@v1.5.3 && go mod tidy` |
| 4 | No artifact signing in goreleaser | `.goreleaser.yaml` | Small | Add `signs:` or `sboms:` block with `cosign` to enable supply-chain verification |
| 5 | `context.Background()` without timeout for pre-session K8s calls in `capture` | `cmd/capture.go:71,80` | Trivial | Use a short timeout context for the pod-get and RBAC pre-flight calls |

### Themes
- Error messages are too verbose in JSON output: internal error strings from the K8s API and AWS SDK are forwarded without sanitization, making the JSON report a potential information-disclosure vector when shipped to log aggregators.
- The build/release pipeline has no artifact signing or SBOM generation, which is a supply-chain hygiene gap for a security tool used in production clusters.

---

## Detailed Findings

### Critical

None.

---

### Important

**[I1] Raw Kubernetes and AWS API errors forwarded into structured JSON output**
- **Location:** `cmd/diagnose.go:52, 68, 83, 90, 97, 124, 125, 133, 141, 148, 163, 170, 177`
- **Description:** Every error path in `runDiagnose` calls `err.Error()` and stores the result verbatim in `DiagnoseCheckResult.Error`, which is serialised to JSON when `--output json` is used. Kubernetes API errors can contain the cluster API server's full URL (e.g., `https://ABCDEF.gr7.us-east-1.eks.amazonaws.com`); AWS SDK errors can contain region identifiers, service endpoints, and request IDs. When `kdiag diagnose` output is forwarded to a log aggregator, SIEM, or CI artifact, these details are persistently disclosed to anyone with read access to those systems.
- **Impact:** Cluster endpoint exposure, AWS account context, and internal IP addresses can appear in logs. In a shared platform team this may widen the blast radius of a future breach — an attacker with log-read access learns the EKS endpoint and region without needing cluster credentials. (CWE-209: Information Exposure Through an Error Message)
- **Remediation:** Sanitize the error string before storing it. For K8s API errors use `apierrors.ReasonForError(err)` or a fixed-format string like `"API request failed (forbidden)"`. For AWS SDK errors check for `*smithy.OperationError` and surface only the operation name and HTTP status code, not the full message. A practical middle ground: truncate the error to 120 characters and strip URL-like substrings.
- **Evidence:**
  ```go
  // cmd/diagnose.go:52
  DiagnoseCheckResult{Name: "inspect", Severity: SeverityError,
      Summary: "pod inspection failed", Error: err.Error()}

  // cmd/diagnose.go:124 — AWS SDK error passes through verbatim
  DiagnoseCheckResult{Name: "cni", Severity: SeverityError,
      Summary: "failed to create EC2 client", Error: ec2Err.Error()}
  ```

**[I2] `dig` raw output unconditionally included in JSON responses**
- **Location:** `pkg/dns/dns.go:20`, `cmd/dns.go:159`
- **Description:** `DNSResult.RawOutput` is tagged `json:"raw_output,omitempty"` and is always populated with the full stdout of `dig` (`stdout.String()`), regardless of whether verbose mode is active. The raw dig output can contain the CoreDNS server IP, DNS TTLs, and EDNS options. In the table output path `RawOutput` is never printed, so it is silently included only in JSON mode — a user may not realize the extent of what is serialised.
- **Impact:** This is bounded to what a cluster operator could already see with `kubectl exec dig`, so the impact is low for most deployments. However, shipping this output to a log aggregator reveals internal cluster DNS infrastructure to anyone with log access. (CWE-200: Exposure of Sensitive Information)
- **Remediation:** Conditionally populate `RawOutput` only when `IsVerbose()` returns true, or rename the field to `verbose_output` and document its intended use for debugging.
- **Evidence:**
  ```go
  // cmd/dns.go:159 — always populated regardless of verbose flag
  result := dns.DNSResult{
      ...
      RawOutput: stdout.String(),
  }
  ```

---

### Advisory

- **No release artifact signing.** `.goreleaser.yaml` generates SHA256 checksums (`checksums.txt`) but does not sign releases or produce SBOMs. For a security tool deployed into production clusters, `cosign` signing and an SBOM (`sboms:` block) would enable downstream consumers to verify supply-chain integrity. This is a hardening gap, not a vulnerability. Add a `signs:` block referencing `cosign` and a `sboms:` block to `.goreleaser.yaml`.

- **`gorilla/websocket` v1.5.0 is one minor version behind (v1.5.3 is current).** `cmd/capture.go` and `pkg/k8s/exec.go` use WebSocket-based exec/attach via `k8s.io/client-go`, which transitively depends on `gorilla/websocket`. No CVE has been published against v1.5.0 as of this audit date, but v1.5.1 included a DoS fix (CVE-2024-31447 does not apply to client-side use). Routine `go get github.com/gorilla/websocket@v1.5.3` is low-effort and eliminates the gap. (`go.mod:49`)

- **`golang.org/x/net` is v0.30.0; current is v0.51.0.** Multiple HTTP/2 DoS fixes (CVE-2023-44487 CONTINUATION flood, CVE-2023-39325) were backported to the `x/net` series. The current pinned version postdates those fixes, but the gap to v0.51.0 is substantial (21 minor versions). Routine update is recommended: `go get golang.org/x/net@latest`. (`go.mod:70`)

- **`context.Background()` without timeout in `capture.go` pre-flight calls.** The pod-existence check (`capture.go:71`) and RBAC preflight (`capture.go:80`) use `context.Background()` directly with no timeout. If the API server is unreachable these calls can hang indefinitely until the OS TCP timeout fires. Use `context.WithTimeout(context.Background(), GetTimeout())` for these two calls, matching the pattern used by all other commands. (Not a security vulnerability — a reliability gap that could leave a terminal session hung.)

- **Node shell cleanup pod deletion uses `context.Background()` with no timeout.** `cmd/shell.go:212` creates a fresh `context.Background()` for `DeleteNodeDebugPod` in the deferred cleanup function. This is intentionally decoupled from the session context (correct), but has no timeout. A slow or unavailable API server will block the cleanup goroutine. Add a 30-second timeout: `cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second); defer cleanupCancel()`.

- **`PodENIAnnotation.PrivateIP` and `SubnetCIDR` are included in JSON output** (`pkg/aws/sg.go:41,43`). These fields are parsed from the `vpc.amazonaws.com/pod-eni` annotation but only used internally to look up the ENI ID — they are not included in `SGReport.SecurityGroups`. Confirmed safe: the `PodENIAnnotation` struct is an internal parse target not directly serialised in output. No action needed.

- **`captureInterface` flag has no validation whitelist.** `cmd/capture.go:39` accepts any string for `--interface` and passes it as the `-i` argument to `tcpdump`. Since the command runs via `ExecInContainer` (which passes args as a Go slice, never through a shell), no injection is possible. However, an operator could pass a garbage value causing `tcpdump` to fail without a clear error. A simple pattern check (`^[a-zA-Z0-9._:-]{1,32}$`) would improve the error message, but this is cosmetic.

---

## Cross-Component Analysis

**Error propagation architecture.** The `diagnose` command aggregates results from five subsystems (inspect, dns, netpol, cni, sg) and stores errors in a shared `DiagnoseCheckResult.Error` string field. Each subsystem returns Go errors that contain the full error chain from the underlying library (K8s client-go, AWS SDK). This design means that a single change to `NewEC2Client` error formatting would silently change what appears in the JSON output of `diagnose`. The fix should be applied at the `runDiagnose` aggregation layer with a helper function like `sanitizeError(err error) string`, rather than at each individual call site.

**Verbose flag and data disclosure.** The `--verbose` flag gates internal diagnostic logging to stderr (e.g., `[kdiag] resolving source pod...`), but does not gate what goes into JSON stdout. `dns.DNSResult.RawOutput` and `DiagnoseCheckResult.Error` are always populated regardless of the verbose flag. There is an implicit contract that "stderr = diagnostic noise, stdout = structured data for machines" — the current implementation breaks this contract by embedding variable-depth error strings into stdout JSON. Applying a consistent `sanitizeError` helper at each JSON-emitting boundary would restore the contract.

**AWS credential error message.** `pkg/aws/client.go:34` wraps the SDK credential error with `%w` and appends remediation guidance. The wrapped error message from the SDK (`NoCredentialProviders`) does not contain credential material — confirmed safe. The remediation hint `export AWS_ACCESS_KEY_ID=...` is documentation, not a disclosure risk.

**Node debug pod privilege level.** `pkg/k8s/nodedbg.go` creates a pod with `HostPID: true`, `HostNetwork: true`, `HostIPC: true`, `Privileged: true`, and the host filesystem mounted at `/host`. This is the intended design for a node shell tool and is architecturally correct — `runNodeShell` in `cmd/shell.go:184` gates creation on a `pods/create` RBAC check. The risk is that operators may not realize the full scope of privilege they are granting; a warning banner printed before attaching would be good practice, but is not a code security issue.

**RBAC pre-flight coherence.** All commands that create ephemeral containers call `CheckEphemeralContainerRBAC` before attempting to create them. `runNodeShell` calls `CheckSingleRBAC` for `pods/create`. Both checks happen before any K8s write operations. The pre-flight checks are consistent across all commands — no command bypasses them.

---

## Analysis Coverage

| Area | Checked | Notes |
|------|---------|-------|
| Code Security (OWASP) | Yes | All `.go` files under `cmd/` and `pkg/` reviewed. No injection, XSS, or auth bypass found. |
| Secrets & Credentials | Yes | Full grep scan across all files; no hardcoded secrets. `.goreleaser.yaml` uses env-var template for tap token (correct). |
| Dependencies | Yes | `go.mod` reviewed; `go list -m -u` run. No known CVEs in pinned versions. Two packages are behind current but not CVE-exposed. |
| Infrastructure as Code | N/A | No Terraform or Ansible files present. |
| Docker/Container | N/A | No Dockerfile present. |
| Configuration | Yes | `.goreleaser.yaml` reviewed. No debug flags, no hardcoded tokens. Missing: artifact signing. |

---

## Dependency Status

| Package | Pinned Version | Latest | Known CVEs | Status |
|---------|---------------|--------|-----------|--------|
| `golang.org/x/net` | v0.30.0 | v0.51.0 | None against v0.30.0 (HTTP/2 DoS fixes are in versions prior to v0.30.0) | OK |
| `gorilla/websocket` | v1.5.0 | v1.5.3 | None confirmed against v1.5.0 in client-side use | WARN (update recommended) |
| `gogo/protobuf` | v1.3.2 | v1.3.2 | CVE-2021-3121 affects unmarshaling of untrusted input; kdiag only uses this as a K8s client dep and does not unmarshal external protobuf data | OK |
| `k8s.io/client-go` | v0.32.3 | v0.35.2 | None against v0.32.3 | OK |
| `aws-sdk-go-v2` components | Various | Current at time of go.mod | None known | OK |

---

## IaC Findings

No Terraform, Ansible, or Dockerfile present. The only release infrastructure is `.goreleaser.yaml`.

| Resource | Check | Status |
|----------|-------|--------|
| `.goreleaser.yaml` builds | `CGO_ENABLED=0` — static binary, no CGO attack surface | PASS |
| `.goreleaser.yaml` ldflags | `-s -w` strip debug symbols; no sensitive data injected | PASS |
| `.goreleaser.yaml` checksums | SHA256 `checksums.txt` generated | PASS |
| `.goreleaser.yaml` signing | No `signs:` or `sboms:` block configured | FAIL |
| `.goreleaser.yaml` token | `{{ .Env.HOMEBREW_TAP_TOKEN }}` — env var template, not hardcoded | PASS |
| `.goreleaser.yaml` archives | Only `README.md` and `LICENSE` bundled alongside binary | PASS |
