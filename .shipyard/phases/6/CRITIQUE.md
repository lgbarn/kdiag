# Plan Critique: Phase 6
**Phase:** 6 — Concurrent ENI Queries
**Date:** 2026-03-11
**Type:** plan-review (pre-execution feasibility stress test)
**Plan reviewed:** PLAN-1.1.md

---

## Per-Check Findings

### Check 1: File paths exist

All five files named in the plan are confirmed present:

| File | Exists |
|------|--------|
| `pkg/aws/eni.go` | YES — confirmed by `ls` |
| `pkg/aws/eni_test.go` | YES |
| `cmd/eks/node.go` | YES |
| `cmd/eks/cni.go` | YES |
| `cmd/diagnose.go` | YES |

**Result: PASS**

---

### Check 2: API surface matches

**`ComputeNodeUtilization` location:** The plan states the function is at `pkg/aws/eni.go:148`. Confirmed — declaration is at line 148:

```
func ComputeNodeUtilization(ctx context.Context, api EC2API, nodes []NodeInput, prefixDelegation bool) ([]NodeUtilization, []ENISkippedNode, error)
```

**Call sites — 3 callers described in Task 2:**

| Plan claim | Actual location | Match |
|------------|----------------|-------|
| `cmd/eks/node.go:142` | `grep` confirms line 142 | YES |
| `cmd/eks/cni.go:150` | `grep` confirms line 150 | YES |
| `cmd/diagnose.go:348` | `grep` confirms line 348 | YES |

**Test count — Task 2 says "7 `TestComputeNodeUtilization_*` test calls":**
`grep -c "^func TestComputeNodeUtilization"` returns **7**. Plan count is correct.

**Result: PASS**

---

### Check 3: Verification commands are runnable

Ran all four verification commands against the pre-execution baseline:

| Command | Exit code | Output |
|---------|-----------|--------|
| `go build ./...` | 0 | (no output — clean) |
| `go test ./...` | 0 | 5 packages pass, 2 have no test files |
| `go test -race ./pkg/aws/...` | 0 | `ok github.com/lgbarn/kdiag/pkg/aws 1.370s` |
| `go vet ./...` | 0 | (no output — clean) |

All commands are runnable and establish a clean baseline. The plan's verification block is concrete and executable.

**Result: PASS**

---

### Check 4: Task count

PLAN-1.1.md contains exactly **3 tasks**. Within the 3-task limit.

**Result: PASS**

---

### Check 5: Complexity — files touched

| Task | Files | Count |
|------|-------|-------|
| Task 1 | `pkg/aws/eni.go` | 1 |
| Task 2 | `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`, `pkg/aws/eni_test.go` | 4 |
| Task 3 | `pkg/aws/eni_test.go` | 1 (already counted) |

Total unique files touched: **5**. Scope is bounded and manageable.

**Result: PASS**

---

## Issues Found

### Issue 1 (HIGH): Plan design diverges from ROADMAP.md deliverables

This is the most significant finding. The ROADMAP.md for Phase 6 specifies a **new function** as the primary deliverable:

> `ListNodeENIsConcurrent(ctx, api, nodes []EligibleNode, concurrency int) (map[string]*NodeENIInfo, error)`

And the ROADMAP success criterion states:

> `grep -n "ListNodeENIs(" cmd/eks/node.go cmd/eks/cni.go` returns zero hits (all call sites use the concurrent variant)

PLAN-1.1.md instead adds a `concurrency int` parameter directly to the existing `ComputeNodeUtilization` signature. The callers (`node.go`, `cni.go`) continue to call `ComputeNodeUtilization`, not a `ListNodeENIsConcurrent` function. Under the plan's design the roadmap's grep success criterion (`ListNodeENIs(` zero hits in caller files) would still **pass** — neither file calls `ListNodeENIs` directly today, so the grep already returns zero — but only vacuously, not because of the intended architectural change.

However, CONTEXT-6.md (`/Users/lgbarn/Personal/kdiag/.shipyard/phases/6/CONTEXT-6.md`) explicitly records a design decision overriding the roadmap:

> **Decision:** Add concurrency directly inside `ComputeNodeUtilization` rather than a separate wrapper
> **Rationale:** Simpler API — callers don't need to change.

The CONTEXT-6.md is dated and co-located with the phase, and it records a deliberate trade-off. The plan is internally consistent with CONTEXT-6.md.

**Conclusion:** The plan is aligned with the phase context document. The ROADMAP.md deliverable description is stale relative to the design decision. The executor and reviewer must be aware that the grep success criterion in the ROADMAP (`ListNodeENIs(` in caller files) was already vacuously true before this phase and remains so — it does not validate anything meaningful. A better post-execution check is:

```bash
grep -n "ListNodeENIs(" pkg/aws/eni.go
```
which should show exactly one call site (inside the goroutine in `ComputeNodeUtilization`), confirming the concurrent wrapper is in place.

---

### Issue 2 (MEDIUM): Task 3 partial-error mock requires per-instance dispatch — not covered by plan

Task 3's `TestComputeNodeUtilization_ConcurrentPartialError` requires the mock's `describeNetworkInterfaces` function to return success for node-1 and node-3, but failure for node-2. The `mockEC2API` struct at `/Users/lgbarn/Personal/kdiag/pkg/aws/ec2iface_mock_test.go:11` holds a single function field for `DescribeNetworkInterfaces` — there is no per-call state or routing logic built in.

The plan does not describe how this dispatch will be implemented inside the mock closure. It is achievable (the closure can inspect `params.Filters[0].Values[0]` to check the instance ID, or use an `atomic.Int32` call counter), but the plan leaves the implementation entirely implicit. A builder reading only the plan could write a mock that always fails and pass the wrong assertion, or write a mock that always succeeds and fail to test the partial-error path.

**Recommendation:** Task 3 should add one sentence describing the mock dispatch mechanism, e.g. "The mock closure should inspect `params.Filters[0].Values[0]` to identify the instance and return an error only when the value equals `i-002`."

---

### Issue 3 (LOW): `sync` import not currently present in `eni.go` — plan correctly calls this out

The current `pkg/aws/eni.go` imports are `context`, `fmt`, and three AWS SDK packages. `sync` is absent. The plan correctly says "Add `sync` to imports" as step 2 of Task 1. No issue with the plan — flagged here only for the builder's awareness.

---

### Issue 4 (LOW): Non-deterministic result order creates implicit test contract gap

The plan notes that result order from the concurrent implementation is non-deterministic and states "callers already iterate results without depending on order." This is correct for the caller code. However, existing tests in `eni_test.go` that reference `utils[0]` by index (e.g. `TestComputeNodeUtilization_OK` at line 257, `TestComputeNodeUtilization_Warning` at line 295) pass `concurrency=0` (serial) per the plan, so they are unaffected. The two new tests in Task 3 are described as order-independent. No action required — the plan handles this correctly — but the builder should confirm that no existing test uses index access with `concurrency > 0`.

---

## Verification Commands Assessment

The four commands in the plan's Verification block are all concrete, runnable, and exit-code checkable. No vague criteria present. The post-execution verification block is adequate with one suggested addition:

```bash
grep -n "ListNodeENIs(" pkg/aws/eni.go
# Expected: exactly one hit, inside the goroutine body
```

This ties the implementation back to the architectural intent more directly than the ROADMAP grep (which was vacuously satisfied before the change).

---

## Verdict

**CAUTION**

The plan is technically feasible and internally consistent. All file paths exist, the function location and all call sites match exactly, the baseline builds and tests are clean, the task count is within limits, and the verification commands are runnable. The plan correctly follows the CONTEXT-6.md design decision.

Two items require attention before execution:

1. **Confirm with the author** that CONTEXT-6.md's design decision (modify `ComputeNodeUtilization` signature) is the accepted override of the ROADMAP's `ListNodeENIsConcurrent` deliverable. If so, annotate the ROADMAP to avoid future confusion. If not, the plan needs revision to introduce the new function.

2. **Task 3 mock dispatch is underspecified.** The partial-error test requires per-instance routing in the `describeNetworkInterfaces` mock closure. The plan should add a concrete implementation note so the builder cannot accidentally write a non-discriminating mock that produces a false PASS.

Neither issue blocks execution if the design decision is confirmed; Issue 2 in particular is a low effort fix inside the test closure. However, shipping without clarifying Issue 1 means the ROADMAP success criterion remains ambiguous.
