# Changelog

All notable changes to kdiag are documented in this file.

## [0.5.1] - 2026-04-09

### Fixed
- BPF capture filter split into separate arguments for tcpdump (multi-word filters like `tcp and port 443` now work)
- `diagnose` reports error instead of silently omitting refs/ingress checks when pod fetch fails
- `diagnose` refs check reports non-NotFound errors (RBAC/timeout) instead of silently counting them as verified
- `diagnose` and `health` commands use 3x base timeout to prevent deadline exceeded on large clusters
- `--protocol` flag no longer silently overridden by service port auto-detection in `connectivity`
- Image validation regex updated to match OCI Distribution Spec (uppercase domains, double separators in paths)
- Signal handler goroutine leak in `logs` command on normal exit
- Multi-pod log output no longer interleaves mid-line (shared mutex across writers)
- `DescribeInstanceTypes` batched in chunks of 100 with input deduplication
- Ephemeral debug containers use bounded `sleep 3600` instead of `sleep infinity`
- Shell pre-flight API calls bounded with timeout context (previously hung on unreachable API server)
- Node debug pod name truncation preserves random suffix for uniqueness
- Capture context creation simplified to single if/else (no double defer cancel)

### Changed
- Node debug pod ActiveDeadlineSeconds set to 2 hours (safety net for orphaned pods after crash/disconnect)

## [0.5.0] - 2026-03-12

### Added
- Windows cross-compilation support (amd64 and arm64)
- Windows builds in CI pipeline and goreleaser releases (.zip format)
- Platform-specific terminal resize handling (SIGWINCH on Unix, no-op on Windows)

## [0.4.0] - 2026-03-11

### Added
- `--profile` and `--region` flags on all commands (moved from EKS-only to global flags)
- IPv6, loopback, and link-local address classification in `diagnose` and `eks endpoint`
- `--status` flag validation: now requires `--show-pods` and must be one of EXHAUSTED, WARNING, or OK
- Bounded concurrency for ENI queries — parallel node lookups with configurable goroutine pool (default 10)

### Fixed
- Duplicate RBAC permission check removed from `shell` ephemeral container error path
- `EnrichWithVpcEndpoints` now returns errors instead of silently swallowing them
- `findIngressesForPod` returns errors instead of silently returning nil
- Discarded `WriteString` error in `eks node` summary output now handled
- Dead `IsForbidden` branch in `shell` command collapsed (was unreachable after pre-flight RBAC check)
- Removed dead `uniqueKeys` function and unnecessary `node := node` loop variable captures

### Changed
- Extracted shared `ComputeNodeUtilization` function — eliminates triplicate ENI utilization logic across `eks node`, `eks cni`, and `diagnose`

## [0.3.0] - 2026-03-07

### Added
- `kdiag ingress <name>` command — inspect Ingress rules, backends, TLS secrets, and controller health
- `kdiag eks endpoint` command — check VPC endpoints for AWS services (private vs public DNS resolution)
- ConfigMap/Secret reference validation in `kdiag diagnose` — detects missing ConfigMaps and Secrets referenced by pod specs
- Ingress check integrated into `kdiag diagnose` summary
- Claude Code skill with 8 troubleshooting playbooks and 3 bundled diagnostic scripts (`pod-triage.sh`, `connectivity-check.sh`, `eks-health.sh`)
- Deployment rollout and node issue playbooks in skill
- `--show-pods` flag on `eks node` command with `--status` filter

### Changed
- Renamed `--aws-profile` to `--profile` and `--aws-region` to `--region` on all `eks` subcommands
- Moved skill from `skill/SKILL.md` to `skill/kdiag/` directory structure for easier installation
- Rewrote diagnostic scripts to use bash arrays instead of string-concatenated flags (prevents word-splitting bugs)

### Fixed
- `extractHostname` scheme detection no longer breaks on hostnames starting with 'h' — uses `strings.Contains(host, "://")` instead of character comparison
- `eks endpoint` command validates required positional arguments before execution

## [0.2.0] - 2026-03-06

### Added
- `--show-pods` flag on `kdiag eks node` to list pods per node with daemonset vs workload breakdown
- `--status` filter for `eks node --show-pods` to show pods only on nodes matching a status (e.g., `EXHAUSTED`)

## [0.1.0] - 2026-03-06

### Added
- Initial release
- `kdiag diagnose` — run all checks and get a severity-ranked summary
- `kdiag inspect` — container states, restart counts, events, owner chain
- `kdiag health` — cluster-wide node and system pod health
- `kdiag dns` — DNS resolution test and CoreDNS health check
- `kdiag connectivity` — TCP/HTTP reachability between pods
- `kdiag trace` — full network path mapping (pod to service to endpoints to nodes)
- `kdiag netpol` — NetworkPolicy analysis showing what affects a pod
- `kdiag logs` — tail logs from pods, deployments, or label selectors
- `kdiag shell` — drop a debug shell (netshoot) into a running pod
- `kdiag capture` — live packet capture with JSON, text, or pcap output
- `kdiag eks cni` — VPC CNI DaemonSet health and per-node IP exhaustion
- `kdiag eks sg` — security groups attached to a pod's ENI
- `kdiag eks node` — node metadata (instance type, AZ, ENI/IP capacity)
- Support for `--output json`, `--namespace`, `--context`, and `--kubeconfig` on all commands
- kubectl plugin support via symlink (`kubectl-kdiag`)
