# kdiag — Concerns Cleanup

## Description
Address all actionable technical concerns identified during codebase mapping of kdiag v0.3.0. This iteration focuses on fixing real bugs (silent error swallowing, N+1 API calls), eliminating code duplication (ENI utilization triplicate), and closing minor error-handling and validation gaps across the CLI.

## Goals
1. Fix silent error swallow in `EnrichWithVpcEndpoints` — return error to caller
2. Replace serial N+1 EC2 `DescribeNetworkInterfaces` calls with a bounded goroutine pool
3. Deduplicate ENI utilization logic into a single shared function in `pkg/aws/eni.go`
4. Fix `findIngressesForPod` returning nil on API errors (false-negative diagnose results)
5. Fix `checkControllerHealth` conflating API errors with "no pods found"
6. Remove duplicate RBAC check on shell→ephemeral path
7. Enforce `--status` flag requiring `--show-pods` on `eks node`
8. Add IPv6 private range support to `ClassifyIP`
9. Fix discarded write error in `cmd/eks/node.go`

## Non-Goals
- Transitive dependency upgrades (golang/protobuf, httpcache) — upstream owned
- Privileged debug pod design — intentional, mitigated by RBAC
- Unbound attach context — intentional for interactive sessions
- CI pipeline changes (golangci-lint, govulncheck) — separate effort
- Homebrew tap enablement — infrastructure, not code
- Structured logging overhaul — too large for this iteration
- BPF filter or image regex changes — currently safe

## Requirements

### Error Handling
- `EnrichWithVpcEndpoints` must return `([]EndpointResult, error)` instead of silently dropping the error
- `cmd/eks/endpoint.go` must handle the new error return and display it to the user
- `findIngressesForPod` must return errors from `Services().List()` and `Ingresses().List()` to callers
- `checkControllerHealth` must distinguish API errors from genuine absence of controller pods
- `cmd/eks/node.go` must handle `os.Stdout.WriteString` error consistently with the rest of the codebase

### Performance
- EC2 `DescribeNetworkInterfaces` calls must be concurrent with a bounded goroutine pool (e.g., semaphore of 10)
- Both `cmd/eks/node.go` and `cmd/eks/cni.go` must use the concurrent approach

### Code Deduplication
- Extract ENI utilization logic (classify nodes → batch instance type limits → query ENIs → compute utilization %) into a shared function in `pkg/aws/eni.go`
- All three callers (`cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`) must use the shared function
- The 85% exhaustion threshold must be defined once

### Validation
- `eks node --status` must return an error if `--show-pods` is not also set

### IPv6
- `ClassifyIP` must recognize `fc00::/7` (unique local addresses) as private

## Non-Functional Requirements
- All changes must have unit tests
- Existing tests must continue to pass
- No new dependencies — use stdlib `sync` for concurrency

## Success Criteria
- `go build ./...` passes
- `go test ./...` passes with all new and existing tests
- `EnrichWithVpcEndpoints` errors propagate to CLI output
- ENI utilization logic exists in exactly one place
- N+1 EC2 calls replaced with concurrent bounded pool
- `--status` without `--show-pods` produces a clear error

## Constraints
- Go 1.25, cobra, existing project structure
- No breaking changes to CLI flags or output format (except new error messages)
- Maintain backward compatibility of `pkg/aws` public API where possible (signature changes are acceptable when fixing bugs)
