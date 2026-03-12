# Documentation Report
**Phase:** 5 — ENI Utilization Consolidation + Error Propagation Fixes
**Date:** 2026-03-11

## Summary

- API/Code docs: 5 public interfaces documented (3 new types, 1 new function, 1 signature change)
- Architecture updates: 4 sections updated (`eni.go`, `endpoint.go` new section, `eks cni` data flow, `eks node` data flow), `EC2API` interface updated
- User-facing docs: 3 command docs updated (`diagnose.md`, `eks-node.md`, `eks-cni.md`)

---

## API Documentation

### `pkg/aws/eni.go` — New types and function
- **File:** `pkg/aws/eni.go`
- **Public interfaces:** 4 (3 types + 1 function)
- **Documentation status:** Added to `docs/architecture.md`

#### `NodeInput`
Input descriptor passed to `ComputeNodeUtilization`. Fields: `Name` (Kubernetes node name), `InstanceID` (EC2 instance ID), `InstanceType`.

#### `ENISkippedNode`
Returned when a node's ENI query fails but processing should continue. Fields: `NodeName`, `Reason` (the underlying error string).

#### `NodeUtilization`
Full utilization snapshot for one node. All fields carry JSON snake_case tags matching the existing output format used by `eks node` and `eks cni`. Key fields: `UtilizationPct` (integer percent), `Status` (`"OK"` / `"WARNING"` / `"EXHAUSTED"`).

#### `ComputeNodeUtilization(ctx, api, nodes []NodeInput, prefixDelegation bool, concurrency int) ([]NodeUtilization, []ENISkippedNode, error)`
Replaces three independent inline implementations (in `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`). All callers pass `concurrency: 10`. Error contract is non-obvious and worth documenting:
- `GetInstanceTypeLimits` failure → terminal error, both slices nil
- Per-node `ListNodeENIs` failure → node added to `ENISkippedNode` slice, processing continues
- Empty input → returns empty non-nil slices, no API calls made
- Result order is non-deterministic when `concurrency > 1`

Documented in `docs/architecture.md` under `pkg/aws — AWS Package / eni.go`.

---

### `pkg/aws/endpoint.go` — Signature change + ClassifyIP expansion
- **File:** `pkg/aws/endpoint.go`
- **Public interfaces:** 2 changed
- **Documentation status:** Updated in `docs/architecture.md`; new `endpoint.go` section added

#### `EnrichWithVpcEndpoints` — signature change (breaking for internal callers)
Previous signature: `(...) []EndpointCheckResult`
New signature: `(...) ([]EndpointCheckResult, error)`

The function previously silently swallowed EC2 API errors and returned the input unchanged. It now surfaces the error. The call site in `cmd/eks/endpoint.go` prints a warning to stderr and continues with DNS-only classification when enrichment fails. Documented in `docs/architecture.md`.

#### `ClassifyIP` — expanded ranges
Added loopback (`127.0.0.0/8`), link-local / AWS metadata (`169.254.0.0/16`), IPv6 loopback (`::1/128`), IPv6 ULA (`fc00::/7`), and IPv6 link-local (`fe80::/10`). Doc comment in source updated; `docs/architecture.md` updated to list all covered ranges.

---

### `cmd/ingress.go` — `findIngressesForPod` error propagation
- **File:** `cmd/ingress.go` (internal function, not a public API)
- **Documentation status:** User-facing behavior documented in `docs/commands/diagnose.md`

This is an internal function (`findIngressesForPod`) whose signature change (now returns `error`) has a user-visible effect: Kubernetes API errors during the ingress check are now surfaced in the `diagnose` report as a `warn`-severity check rather than being silently ignored. Updated `diagnose.md` to describe this behavior.

---

## Architecture Updates

### `pkg/aws/eni.go` — ComputeNodeUtilization extracted
- **Change:** Three callers previously duplicated the full ENI utilization pipeline (unique-type dedup, batch limits query, per-node ENI query, utilization math, status classification). This logic is now in one place.
- **Reason:** Eliminate duplication; ensure consistent thresholds and error behavior across `eks node`, `eks cni`, and `diagnose`.
- **Docs updated:** `docs/architecture.md` — added `ComputeNodeUtilization` under `eni.go`, updated data flow diagrams for `kdiag eks cni` and `kdiag eks node`.

### `pkg/aws/endpoint.go` — New section added
- **Change:** `endpoint.go` had no architecture documentation despite containing four public functions used by `kdiag eks endpoint`.
- **Docs updated:** `docs/architecture.md` — added `endpoint.go` subsection under `pkg/aws` covering `ClassifyIP`, `BuildServiceEndpoints`, `CheckEndpointDNS`, and `EnrichWithVpcEndpoints`.

### `EC2API` interface — `DescribeVpcEndpoints` added
- **Change:** The interface in `ec2iface.go` now includes `DescribeVpcEndpoints`, required by `EnrichWithVpcEndpoints`.
- **Docs updated:** `docs/architecture.md` — interface definition block updated.

---

## User Documentation

### `docs/commands/diagnose.md` — Check table incomplete
- **Type:** Command reference update
- **Status:** Updated
- **Change:** The check table was missing the `refs` and `ingress` checks. Both are always run when a pod is found. Added both rows to the table. Added a paragraph describing the new error-surfacing behavior for the `ingress` check: API errors produce a `warn`-severity result rather than aborting.

### `docs/commands/eks-node.md` — Verbose skip warnings
- **Type:** Command reference update
- **Status:** Updated
- **Change:** The verbose example existed but did not describe what `--verbose` actually prints for skipped nodes. Added the stderr format line so operators know what to look for.

### `docs/commands/eks-cni.md` — Verbose skip warnings
- **Type:** Command reference update
- **Status:** Updated
- **Change:** Same as `eks-node.md` — the `--verbose` example lacked a description of the stderr output format for skipped nodes.

---

## Gaps Resolved

### `docs/commands/eks-endpoint.md` — Created
The `kdiag eks endpoint` command was added in a prior phase but had no command reference doc. Created `docs/commands/eks-endpoint.md` covering synopsis, two-phase check behavior, table/JSON output formats, IAM requirements, and troubleshooting. Updated `docs/README.md` to include `eks endpoint` in both the command table and the command reference link list.

### `diagnose.md` JSON example — Fixed
The JSON output example previously showed 5 checks; `diagnose` runs 7 (`inspect`, `refs`, `dns`, `netpol`, `ingress`, `cni`, `sg`). Updated the example to include `refs` and `ingress` entries and corrected `summary.total` from `5` to `7`.

---

## Remaining Recommendations

1. **Consider documenting `ComputeNodeUtilization` error contract in a developer guide** — the terminal vs. non-terminal error distinction (limits failure kills the call; per-node ENI failure only skips that node) is subtle and will matter to anyone writing a new caller of this function. The current Godoc covers it; a developer guide entry would add cross-reference context.

2. **`diagnose.md` RBAC table is incomplete** — the table lists `list networkpolicies` and `get daemonsets` but is missing `list services` and `list ingresses`, which are required by the `refs` and `ingress` checks respectively. This is a pre-existing gap not introduced in Phase 5.
