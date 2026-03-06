# kdiag diagnose

Run all diagnostic checks against a pod and report pass/warn/fail status.

## Usage

```sh
kdiag diagnose <pod> [-n namespace] [-o table|json]
```

## Description

The `diagnose` command runs a battery of checks against a target pod and produces a consolidated report with per-check severity levels:

| Check | What it does |
|-------|-------------|
| **inspect** | Pod phase, container states, restart counts |
| **dns** | CoreDNS pod health (ready/not-ready count) |
| **netpol** | NetworkPolicies selecting the pod |
| **cni** | AWS VPC CNI DaemonSet health and IP exhaustion (EKS only) |
| **sg** | Security groups attached to the pod's ENI (EKS only) |

On non-EKS clusters, the `cni` and `sg` checks are skipped automatically.

## Severity Levels

| Level | Meaning |
|-------|---------|
| `pass` | Check passed with no issues |
| `warn` | Potential issue detected (e.g., some CoreDNS pods not ready) |
| `fail` | Definite problem found |
| `error` | Check could not complete (e.g., missing permissions) |
| `skipped` | Check not applicable (e.g., EKS checks on non-EKS cluster) |

## Exit Codes

- `0` — All checks passed or warned
- `1` — One or more checks failed (severity `fail`)

## Examples

```sh
# Table output (default)
kdiag diagnose my-pod -n production

# JSON output for CI/CD pipelines
kdiag diagnose my-pod -o json

# With verbose logging
kdiag diagnose my-pod -v
```

## JSON Output

```json
{
  "pod": "my-pod",
  "namespace": "production",
  "is_eks": true,
  "checks": [
    {"name": "inspect", "severity": "pass", "summary": "all 2 container(s) running normally"},
    {"name": "dns", "severity": "pass", "summary": "2/2 CoreDNS pod(s) ready"},
    {"name": "netpol", "severity": "pass", "summary": "3 NetworkPolicy/ies matched"},
    {"name": "cni", "severity": "pass", "summary": "DaemonSet healthy; 0 node(s) with exhausted IPs"},
    {"name": "sg", "severity": "pass", "summary": "4 security groups retrieved"}
  ],
  "summary": {"total": 5, "pass": 5, "warn": 0, "fail": 0, "error": 0, "skipped": 0}
}
```

## RBAC Requirements

| Verb | Resource |
|------|----------|
| get | pods |
| list | pods (kube-system), networkpolicies |
| get | daemonsets (kube-system, for `aws-node`) |
| list | nodes |

## IAM Requirements (EKS)

- `ec2:DescribeInstances`
- `ec2:DescribeNetworkInterfaces`
- `ec2:DescribeSecurityGroups`
- `ec2:DescribeInstanceTypes`
