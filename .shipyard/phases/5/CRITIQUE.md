# Plan Critique — Phase 5
**Date:** 2026-03-11
**Plans reviewed:** PLAN-1.1 (Wave 1), PLAN-1.2 (Wave 1), PLAN-2.1 (Wave 2)
**Baseline:** `go build ./...` exits 0; `go test -count=1 ./...` all packages pass.

---

## Task A: Coverage Check

| Phase 5 Goal | Covered By | Status |
|---|---|---|
| Goal 1: Fix EnrichWithVpcEndpoints silent error swallow | PLAN-1.2 Bug A | COVERED |
| Goal 3: Deduplicate ENI utilization logic | PLAN-1.1 + PLAN-2.1 | COVERED |
| Goal 4: Fix findIngressesForPod returning nil on API errors | PLAN-1.2 Bug C | COVERED |
| Goal 5: Fix checkControllerHealth conflating API errors with "no pods" | NONE | NOT COVERED |
| Goal 8: Add IPv6 private range to ClassifyIP | PLAN-1.2 Bug B | COVERED |

**Gap — Goal 5 (`checkControllerHealth`):** PLAN-1.2 explicitly excludes this fix, citing RESEARCH.md's lower-priority classification. This is an intentional deferral, not an oversight. The phase requirements list it as Goal 5 with no exemption language. The plans should either include the fix or the roadmap must explicitly exclude it. As written, Goal 5 is unmet by any plan.

---

## Task B: Per-Plan Findings

---

### PLAN-1.1 — ComputeNodeUtilization (Wave 1)

**Files:** `pkg/aws/eni.go`, `pkg/aws/eni_test.go`

#### B1. File Paths Exist

| File | Exists |
|---|---|
| `pkg/aws/eni.go` | YES — confirmed at `/Users/lgbarn/Personal/kdiag/pkg/aws/eni.go` |
| `pkg/aws/eni_test.go` | YES — confirmed at `/Users/lgbarn/Personal/kdiag/pkg/aws/eni_test.go` |

#### B2. API Surface Match

| Symbol Referenced | Found in Code | Notes |
|---|---|---|
| `ListNodeENIs` | YES — `pkg/aws/eni.go:36`, signature matches plan exactly | Correct |
| `GetInstanceTypeLimits` | YES — `pkg/aws/eni.go:79`, signature matches plan exactly | Correct |
| `mockEC2API` | YES — `pkg/aws/ec2iface_mock_test.go:11` | Available for tests |
| `describeNetworkInterfaces` field | YES — mock field at line 14 | Correct |
| `describeInstanceTypes` field | YES — mock field at line 13 | Correct |
| `NodeInput` (new) | NOT YET — to be created | Expected |
| `ENISkippedNode` (new) | NOT YET — to be created | Expected |
| `NodeUtilization` (new) | NOT YET — to be created | Expected |
| `ComputeNodeUtilization` (new) | NOT YET — to be created | Expected |

#### B3. Verify Commands

- Task 1 verify: `go test ./pkg/aws/... 2>&1 | grep -E "^(ok|FAIL|---)" | head -20`
  - RUNNABLE. Note that during TDD phase (before Task 2), this command will show a compile error but the grep pattern `"^(ok|FAIL|---)"` will filter it to zero lines, showing no output. The done criteria says "Tests compile (after Task 2)" which is correct, but the Task 1 verify command produces no output in either the failing or the partially-failing state — it only becomes useful post-Task-2. This is an ambiguity in the verify step, not a blocker.

- Task 2 verify: `go build ./pkg/aws/... && go test ./pkg/aws/...`
  - RUNNABLE. Unambiguous and sufficient.

#### B4. Test Case Math Verification

All seven test case assertions verified by arithmetic:
- OK: `(5 * 100) / 30 = 16` — UtilizationPct=16, Status="OK" ✓
- Warning: `(21 * 100) / 30 = 70` — boundary: `>= 70` → "WARNING" ✓
- Exhausted: `(26 * 100) / 30 = 86` — `>= 85` → "EXHAUSTED" ✓
- PrefixDelegation: `3 * 10 * 16 = 480`; `(26 * 100) / 480 = 5` → "OK", MaxTotalIPs=480 ✓

#### B5. Dependency / Forward Reference Risk

PLAN-1.1 has no dependencies. Its only file set is `pkg/aws/eni_test.go` and `pkg/aws/eni.go`, which are not touched by PLAN-1.2. **No file conflicts with Wave 1 parallel plan.**

#### B6. Banner Style Discrepancy (Minor)

PLAN-1.1 instructs the builder to use `// -------- TestFunctionName` banner style. The actual banner in `pkg/aws/eni_test.go` (lines 17–19) is a three-line box style (`// ---...---` / `// TestListNodeENIs` / `// ---...---`). CONVENTIONS.md describes it as `// -----` banners. The plan's stated style does not match the file. This is a cosmetic inconsistency — the builder will see the existing file and follow what they see. Flag as a wording imprecision, not a blocker.

#### B7. Complexity

2 files, 1 directory (`pkg/aws/`). Low complexity.

---

### PLAN-1.2 — Error Handling + IPv6 (Wave 1)

**Files:** `pkg/aws/endpoint.go`, `pkg/aws/endpoint_test.go`, `cmd/eks/endpoint.go`, `cmd/ingress.go`, `cmd/diagnose.go`

#### B1. File Paths Exist

| File | Exists |
|---|---|
| `pkg/aws/endpoint.go` | YES — confirmed, `EnrichWithVpcEndpoints` at line 77, `ClassifyIP` at line 43 |
| `pkg/aws/endpoint_test.go` | YES — `TestClassifyIP_Private` at line 8, `TestBuildServiceEndpoints` at line 30 |
| `cmd/eks/endpoint.go` | YES — `runEndpoint` with the call site at line 76 |
| `cmd/ingress.go` | YES — `findIngressesForPod` at line 249, `checkControllerHealth` at line 203 |
| `cmd/diagnose.go` | YES — ingress call at line 172, `countExhaustedNodes` at line 328 |

#### B2. API Surface Match

| Symbol Referenced | Found in Code | Notes |
|---|---|---|
| `EnrichWithVpcEndpoints` current signature `[]EndpointCheckResult` | YES — `pkg/aws/endpoint.go:77` matches | Correct |
| `ClassifyIP` loop of 3 CIDRs | YES — `pkg/aws/endpoint.go:44` matches | Correct |
| `findIngressesForPod` returns `([]IngressRuleResult, []IngressTLSResult)` | YES — `cmd/ingress.go:249` matches | Correct |
| `ingressSeverity` signature `(rules, tlsList)` two args | YES — `cmd/diagnose_types.go:167` | Correct |
| `SeverityWarn` constant | YES — `cmd/diagnose_types.go:18` = `"warn"` | Correct |
| `sanitizeError` function | YES — `cmd/diagnose.go:29` (same package) | Correct |
| `IsVerbose()` (capitalized) in diagnose.go | YES — `cmd/diagnose.go:183` uses `IsVerbose()` | Correct — package-level exported function |
| `isVerbose()` (lowercase) in cmd/eks/endpoint.go | YES — `cmd/eks/eks.go:73` | Correct |
| `describeVpcEndpoints` mock field | YES — `pkg/aws/ec2iface_mock_test.go:16` | Available for EnrichWithVpcEndpoints error test |

#### B3. CRITICAL — Incorrect Line Reference in Bug C Description

PLAN-1.2 Bug C states: "Update `cmd/ingress.go` line 241 call site (`runIngress` does not call `findIngressesForPod` directly — only `runDiagnose` does)."

Inspection of `cmd/ingress.go` line 241 shows it is inside `checkControllerHealth` (specifically inside the pod-ready loop, line 238–244 context). There is no call site for `findIngressesForPod` at line 241 of `cmd/ingress.go`. The function `findIngressesForPod` is **defined** at `cmd/ingress.go:249` and **called** only at `cmd/diagnose.go:172`. The plan text is self-contradictory: it says "only `runDiagnose` does" call it (true) while simultaneously saying to "Update `cmd/ingress.go` line 241 call site" (line 241 of ingress.go is not a call site). The builder reading Bug C will be confused about which file/line to update, but the code block that follows (the `ingErr` check pattern) correctly targets `cmd/diagnose.go:172`. The prose narrative is misleading; the XML task id="2" action block correctly identifies the two files to change (`cmd/ingress.go` to change the function signature, `cmd/diagnose.go` to update the call). This is a documentation error in the prose, not a structural plan error — the XML task block is unambiguous.

#### B4. CAUTION — Verify Command Scope in Task 2

Task 2 verify: `go build ./... && go test ./pkg/aws/... ./cmd/...`

The `./cmd/...` pattern runs tests in `github.com/lgbarn/kdiag/cmd` and `github.com/lgbarn/kdiag/cmd/eks`. Confirmed: `cmd/eks` has no test files (`[no test files]`) so this passes trivially for that sub-package. The `cmd` package tests (`cmd/` with test files) will exercise diagnose and ingress code paths with fake clients — this is appropriate coverage. The verify command is sound.

#### B5. CAUTION — `os` Import Already Present in cmd/diagnose.go

PLAN-1.2 Fix C adds `fmt.Fprintf(os.Stderr, ...)` to `cmd/diagnose.go`. The `"os"` package is already imported at `cmd/diagnose.go:8`. No import management needed. However, the plan does not mention checking imports for the `cmd/ingress.go` change. The signature change to `findIngressesForPod` adds a third return value; the function already imports all needed packages (`fmt` for error wrapping). No new imports required. No risk.

#### B6. CAUTION — `ingressSeverity` Signature Unchanged

PLAN-1.2 changes `findIngressesForPod` to return three values. In the happy-path branch of the updated `cmd/diagnose.go` call site, the plan shows calling `ingressSeverity(ingRules, ingTLS)` with two arguments. Verified: `ingressSeverity` accepts exactly two arguments (`cmd/diagnose_types.go:167`). This is compatible. No change needed to `ingressSeverity`.

#### B7. Forward References / File Conflicts

- PLAN-1.2 touches `cmd/diagnose.go` (ingress check block, lines 171–177).
- PLAN-2.1 (Wave 2) also touches `cmd/diagnose.go` (`countExhaustedNodes` body, lines 326–366).
- These are **different functions in the same file**. Since PLAN-2.1 is Wave 2 and depends on PLAN-1.1 (not PLAN-1.2), the execution order is: PLAN-1.1 and PLAN-1.2 in parallel, then PLAN-2.1. The edits are to non-overlapping regions of `cmd/diagnose.go`. No conflict **as long as Wave 2 starts after both Wave 1 plans are complete**. This ordering is correct per the plan metadata.

#### B8. Complexity

5 files, 2 directories (`pkg/aws/`, `cmd/eks/`, `cmd/`). Moderate. Acceptable.

---

### PLAN-2.1 — Wire ComputeNodeUtilization Callers (Wave 2)

**Files:** `cmd/eks/node.go`, `cmd/eks/cni.go`, `cmd/diagnose.go`
**Dependency:** PLAN-1.1 (correct — needs `ComputeNodeUtilization`)

#### B1. File Paths Exist

| File | Exists |
|---|---|
| `cmd/eks/node.go` | YES — `runNode` with loop at lines 139–197 |
| `cmd/eks/cni.go` | YES — `runCNI` with loop at lines 156–209 |
| `cmd/diagnose.go` | YES — `countExhaustedNodes` at lines 328–366 |

#### B2. API Surface Match

| Symbol Referenced | Found in Code | Notes |
|---|---|---|
| `EligibleNode` | YES — `cmd/eks/eks.go:119` | Correct |
| `ClassifyNodes` | YES — `cmd/eks/eks.go:132` | Correct |
| `SkippedNode` | YES — `cmd/eks/eks.go:114` | Correct |
| `awspkg.NodeInput` (new) | NOT YET — from PLAN-1.1 | Correct dependency |
| `awspkg.ComputeNodeUtilization` (new) | NOT YET — from PLAN-1.1 | Correct dependency |
| `awspkg.ENISkippedNode` (new) | NOT YET — from PLAN-1.1 | Correct dependency |
| `eks.ClassifyNodes` called from `countExhaustedNodes` | YES — `cmd/diagnose.go:329` | Already imported |

#### B3. ISSUE — "Map Fields Directly" Is Inaccurate for Utilization

PLAN-2.1 states for node.go: "Map fields directly (all field names correspond)." This is **incorrect for the `Utilization` field**. `NodeUtilization.UtilizationPct` is type `int`. `NodeENIStatus.Utilization` is type `string` (JSON: `"utilization_pct"`). The existing code uses `strconv.Itoa(utilPct)` to convert. The plan does not mention this conversion. A builder who follows "map fields directly" and writes `Status: u.Status, Utilization: u.UtilizationPct` will get a **compile error** because `string` cannot receive `int`.

The same issue applies to `NodeCapacity.Utilization string` in `cni.go` — it also needs `strconv.Itoa(u.UtilizationPct)`.

The `strconv` import is already present in both files. The conversion is a one-liner, but the omission in the plan's prose description could cause confusion. The XML task block for PLAN-2.1 does not provide field-by-field mapping code, only high-level instructions. A careful builder will notice the type mismatch at compile time; a less careful builder relying on the "map directly" language may wonder why it fails to compile.

**Severity: CAUTION.** Not a blocker — the compile error will be immediately obvious — but the plan should be corrected.

#### B4. Done Criteria Grep Command

The done criteria says:
```
grep -n "GetInstanceTypeLimits\|ListNodeENIs" cmd/eks/node.go cmd/eks/cni.go cmd/diagnose.go
should produce zero matches
```

Verified: currently this grep produces **6 matches** (confirmed by inspection). After PLAN-2.1 removes the direct calls, it should produce zero. The grep command syntax is valid and runnable. This is a good, objective done criterion.

#### B5. Line Number References

PLAN-2.1 says to delete from `node.go` "lines 130-197" (GetInstanceTypeLimits call and per-node loop). Inspection confirms:
- Lines 130–136: `GetInstanceTypeLimits` call (actual code at lines 131–136 in current file)
- Lines 138–197: per-node loop
These are approximately correct. The plan references line 130 for a comment line (`// 7. Batch-query`); the actual `limitsMap` assignment starts at line 133. Minor imprecision — the builder will read the file and delete the correct block regardless.

PLAN-2.1 says to delete from `cni.go` "lines 144-209" (GetInstanceTypeLimits and per-node loop). Confirmed: lines 144–209 in cni.go span `typeList` construction, `GetInstanceTypeLimits` call, node loop, and the `nodes`/`ipExhausted` variables. Approximately correct.

#### B6. Behavior Preservation — cni.go `ipExhausted` Accumulation

The plan notes: "If exhausted, append `en.Name` to `ipExhausted` — use `u.NodeName`." This is correct: the refactor changes from `en.Name` (from the loop variable) to `u.NodeName` (from the returned struct). These should be equivalent since `NodeInput.Name` will be set from `en.Name`. No semantic change.

#### B7. `countExhaustedNodes` Signature Preservation

Plan correctly states the signature stays `(ctx, ec2Client, []corev1.Node) (int, error)` unchanged. The `runDiagnose` call at `cmd/diagnose.go:210` requires no update. Confirmed: `cmd/diagnose.go:210` calls `countExhaustedNodes(ctx, ec2Client, nodeList.Items)` — this signature is preserved. ✓

#### B8. IsVerbose vs isVerbose in diagnose.go

PLAN-2.1 says for `countExhaustedNodes`: "Log skipped nodes to stderr when `IsVerbose()`." In `cmd/diagnose.go`, the verbose-check function is `IsVerbose()` (exported, uppercase I). Confirmed at line 183. This matches. ✓

#### B9. Complexity

3 files, 2 directories (`cmd/eks/`, `cmd/`). Moderate but the edits are deletions (removing duplicate arithmetic) plus structured replacements. Low implementation risk.

---

## Cross-Plan File Conflict Matrix

| File | PLAN-1.1 | PLAN-1.2 | PLAN-2.1 | Conflict? |
|---|---|---|---|---|
| `pkg/aws/eni.go` | Wave 1 (writes) | — | — | None |
| `pkg/aws/eni_test.go` | Wave 1 (writes) | — | — | None |
| `pkg/aws/endpoint.go` | — | Wave 1 (writes) | — | None |
| `pkg/aws/endpoint_test.go` | — | Wave 1 (writes) | — | None |
| `cmd/eks/endpoint.go` | — | Wave 1 (writes) | — | None |
| `cmd/ingress.go` | — | Wave 1 (writes) | — | None |
| `cmd/diagnose.go` | — | Wave 1 (lines 171–177) | Wave 2 (lines 326–366) | No conflict — disjoint regions, different waves |
| `cmd/eks/node.go` | — | — | Wave 2 (writes) | None |
| `cmd/eks/cni.go` | — | — | Wave 2 (writes) | None |

Wave 1 plans have fully disjoint file sets. Wave 2 touches `cmd/diagnose.go` in a region untouched by Wave 1. No conflicts.

---

## Summary of Issues

| ID | Plan | Finding | Severity |
|---|---|---|---|
| I-1 | All | Goal 5 (checkControllerHealth) not covered by any plan | CAUTION — intentional deferral but undocumented in plans |
| I-2 | PLAN-1.1 | Task 1 verify command produces no output in TDD failure state (grep pattern filters compile errors to zero lines) | MINOR — verify still works post-Task-2 |
| I-3 | PLAN-1.1 | Banner style description "// -------- TestFunctionName" does not match actual file style (3-line box) | MINOR — cosmetic |
| I-4 | PLAN-1.2 | Bug C prose says "Update cmd/ingress.go line 241 call site" — line 241 of ingress.go is not a call site; the XML task block is correct | MINOR — documentation error in prose only |
| I-5 | PLAN-2.1 | "Map fields directly" instruction omits the required `strconv.Itoa(u.UtilizationPct)` conversion for the `Utilization string` field in both `NodeENIStatus` and `NodeCapacity` | CAUTION — will cause immediate compile error; easy to fix on contact |

---

## Verdict

**CAUTION**

The plans are structurally sound: file paths all exist, function signatures match the codebase, wave ordering correctly respects dependencies, no file conflicts exist between parallel Wave 1 plans, task counts are within bounds (2-2-1), and the verify commands are runnable. The test case math is correct.

Three issues require awareness before execution:

1. **Goal 5 is unmet.** `checkControllerHealth` conflation is intentionally deferred by PLAN-1.2. If the phase requirements treat Goal 5 as mandatory, a new plan must be added or the roadmap must be updated to defer it explicitly.

2. **PLAN-2.1 "map fields directly" is misleading.** The `Utilization` field (`string` in both `NodeENIStatus` and `NodeCapacity`) cannot receive `UtilizationPct` (`int` from `NodeUtilization`) without a `strconv.Itoa()` call. The compile error will be caught immediately, but the builder should be alerted to add the conversion when iterating `utils`.

3. **PLAN-1.2 Bug C prose references a nonexistent call site** ("cmd/ingress.go line 241"). The XML task block is correct. The builder should follow the XML task block, not the prose line reference.

None of these issues are blockers to execution. Proceed with the mitigations listed above.
