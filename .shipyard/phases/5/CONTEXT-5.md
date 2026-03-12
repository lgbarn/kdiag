# Phase 5 Design Decisions

## ENI ComputeNodeUtilization API
- **Decision:** Return a rich struct (`[]NodeUtilization`) with per-node counts, percentages, and status
- **Rationale:** Each caller (node, cni, diagnose) extracts what it needs from the same data
- **The struct should include:** node name, ENI count, ENI limit, utilization percentage, exhaustion status (WARNING/EXHAUSTED/OK)

## Diagnose Ingress Error Handling
- **Decision:** WARN and continue when `findIngressesForPod` hits an API error
- **Rationale:** Don't fail the entire diagnose run for a single check's API error; other checks still provide value
- **Implementation:** Print a warning line to stderr, mark the ingress check as WARN (not PASS/FAIL)

## EnrichWithVpcEndpoints Error Handling
- **Decision:** Return error to caller (decided in brainstorm)
- **Implementation:** Change signature to return `([]EndpointCheckResult, error)`; `cmd/eks/endpoint.go` handles the error

## Concurrency
- **Decision:** Bounded goroutine pool with semaphore (decided in brainstorm)
- **Note:** This is Phase 6 work, but Phase 5's shared function should be designed to be concurrency-safe

## ENI Dedup Location
- **Decision:** Shared function in `pkg/aws/eni.go` (decided in brainstorm)
