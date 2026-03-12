# Milestone Report: Concerns Cleanup

**Completed:** 2026-03-11
**Phases:** 3/3 complete (Phases 5, 6, 7)

## Phase Summaries

### Phase 5: ENI Deduplication and Error Handling
Extracted `ComputeNodeUtilization` shared function in `pkg/aws/eni.go`, eliminating triplicate ENI utilization logic across `cmd/eks/node.go`, `cmd/eks/cni.go`, and `cmd/diagnose.go`. Fixed silent error swallowing in `EnrichWithVpcEndpoints` (now returns error), `findIngressesForPod` (now returns error instead of nil), and `checkEndpointDNS`. Added IPv6 private range classification (`fc00::/7`, `::1/128`, `fe80::/10`) to `ClassifyIP`.

### Phase 6: Concurrent ENI Queries
Added `concurrency int` parameter to `ComputeNodeUtilization` with a bounded goroutine pool using semaphore pattern (`sync.WaitGroup` + `sync.Mutex`). All callers pass `DefaultConcurrency = 10`. Semaphore acquired before goroutine spawn to bound goroutine count. Race detector passes clean.

### Phase 7: Isolated Fixes
Removed duplicate `CheckEphemeralContainerRBAC` call in `cmd/shell.go`. Added `--status` flag validation (requires `--show-pods`, must be EXHAUSTED/WARNING/OK). Fixed discarded `WriteString` error in `cmd/eks/node.go`.

### Post-Phase: Findings Resolution & Flag Migration
Resolved all audit, simplification, and documentation findings across phases 5-7. Moved `--profile` and `--region` flags from `EksCmd` to `rootCmd` so all commands (including `diagnose`) inherit them.

## Key Decisions
- `ComputeNodeUtilization` returns a rich `NodeUtilization` struct; each caller projects the fields it needs
- CNI intentionally does not surface the WARNING tier (only EXHAUSTED)
- `checkControllerHealth` conflation (Goal 5) was intentionally deferred — only affects standalone `kdiag ingress`
- Semaphore acquired before goroutine spawn to bound goroutine count, not just active work

## Documentation Status
- API documentation: Updated for all new types and functions
- Architecture documentation: Updated with concurrency annotations and flag migration
- User guides: New `docs/commands/eks-endpoint.md` created; existing command docs updated
- README updated: `--profile`/`--region` shown as global flags

## Known Issues
- `checkControllerHealth` still conflates API errors with absent pods (deferred Goal 5)
- Phase 5 marked `complete_with_gaps` due to this deferral

## Metrics
- Files modified: 12 Go files
- Total commits: 18
- Lines: +700 / -189
- New tests: 11 (7 ENI utilization, 2 concurrent, 1 endpoint error, 1 IPv6)
