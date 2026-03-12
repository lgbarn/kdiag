# Shipyard Lessons Learned

## [2026-03-07] Phases 1-4: kdiag Coverage Gaps

### What Went Well
- Phased approach worked cleanly: flag rename → new commands → skill updates
- EC2API interface pattern made VPC endpoint testing straightforward with mocks
- Bundled diagnostic scripts (pod-triage, connectivity-check, eks-health) consolidate multi-step workflows into one command

### Surprises / Discoveries
- LSP shows false positives (undefined, missing method) after code generation — always verify with `go build ./...` and `go test ./...`, not the IDE
- Skill-creator `run_loop.py` description optimization requires an Anthropic API key and bills separately from Claude Max subscription
- Skill description with 0% recall in trigger eval — long keyword-heavy descriptions paradoxically reduce triggering; shorter action-oriented descriptions with explicit negative boundaries work better

### Pitfalls to Avoid
- `extractHostname` used `host[0] != 'h'` for scheme detection — fragile, a hostname starting with 'h' breaks it. Use `strings.Contains(host, "://")` instead
- `.gitignore` patterns can block `skill/` directory — always use `git add -f` for skill files
- String-concatenated shell flags (`$FLAGS`) cause word-splitting/glob expansion — use bash arrays and `"${FLAGS[@]}"` expansion

### Process Improvements
- Run `go build ./...` after every code generation phase before claiming success
- For shell scripts: always use arrays for flag accumulation from day one
- Test skill triggering with realistic prompts early, not just content quality

---

## [2026-03-11] Phases 5-7: Concerns Cleanup

### What Went Well
- `ComputeNodeUtilization` extraction cleanly served 3 callers with different needs via a rich return struct — each caller projects only the fields it needs
- Semaphore pattern for bounded concurrency was straightforward with stdlib `sync` only — no external dependencies needed
- Post-build gates (audit, simplifier, documenter) caught 15+ actionable items: dead code, unsanitized errors, dead branches, missing docs

### Surprises / Discoveries
- The `IsForbidden` handler had a dead branch that was logically unreachable — the pre-flight RBAC check made the error-path branch permanently false
- `uniqueKeys` was dead code after the refactor but wasn't caught until the simplifier ran
- Post-build gates were accidentally skipped across all phases — they are not optional and found real issues
- Go 1.25 fixed loop variable semantics, making `node := node` capture patterns unnecessary dead code

### Pitfalls to Avoid
- LSP shows false positives between commits when a function signature changes but callers haven't been updated yet — always verify with `go build`, not LSP diagnostics
- Always run post-build gates (audit, simplifier, documenter) — never skip them, even on small phases
- When moving a flag from a subcommand to root, check all docs (architecture, command references, README) for stale "EKS-only" language

### Process Improvements
- Run audit/simplifier/documenter after every phase build, not just at ship time — catching issues early is cheaper than fixing them in bulk

---
