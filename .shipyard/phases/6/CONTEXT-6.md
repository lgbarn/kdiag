# Phase 6 Design Decisions

## Concurrency Placement
- **Decision:** Add concurrency directly inside `ComputeNodeUtilization` rather than a separate wrapper
- **Rationale:** Simpler API — callers don't need to change. Add a `concurrency int` parameter (0 = serial for backward compat in tests)
- **Implementation:** Semaphore pattern using `make(chan struct{}, concurrency)` with `sync.WaitGroup`

## Error Handling
- Per-node ENI query failures remain non-fatal (append to skipped list)
- Use `sync.Mutex` to protect shared slices (utils, skipped) during concurrent writes
- No new dependencies — stdlib `sync` only
