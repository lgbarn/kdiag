# Changelog

All notable changes to kdiag are documented in this file.

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
