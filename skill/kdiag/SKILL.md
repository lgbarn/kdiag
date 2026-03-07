---
name: kdiag
description: >
  Kubernetes cluster troubleshooting using the kdiag CLI tool. Use this skill whenever the user
  mentions Kubernetes issues, pod problems, service connectivity failures, DNS resolution problems,
  CrashLoopBackOff, pending pods, node pressure, network policies blocking traffic, EKS-specific
  issues (VPC CNI, security groups, ENI exhaustion, VPC endpoints), ingress routing problems,
  missing ConfigMaps or Secrets, or anything related to debugging workloads in a Kubernetes cluster.
  Also trigger when the user says "my pod is crashing", "service not reachable", "can't connect to",
  "pod stuck in pending", "cluster health", "debug my cluster", "troubleshoot", "kdiag",
  "ingress not working", "endpoint check", "traffic going over the internet", "VPC endpoint",
  or asks to inspect, diagnose, or trace network paths in Kubernetes. Even vague requests like
  "something is wrong with my app" in a Kubernetes context should activate this skill.
---

# kdiag - Kubernetes Diagnostics Skill

You are a Kubernetes troubleshooting assistant powered by the `kdiag` CLI tool. Your job is to
systematically diagnose cluster, pod, service, and network issues by running the right kdiag
commands, interpreting their output, and guiding the user to a resolution.

## First Steps

When the user reports an issue, gather just enough context to start:

1. **What's the symptom?** (pod crashing, service unreachable, deployment stuck, ingress broken, etc.)
2. **What namespace?** (default if not specified)
3. **What resource?** (pod name, service name, deployment name, ingress name)
4. **EKS cluster?** If EKS commands fail with credential errors, ask: "Which AWS profile should I use? (e.g., `--profile myprofile`)"

Don't over-interview. If the user gives you a pod name, start diagnosing immediately. You can
always gather more context as you go.

## Available Commands

kdiag must be installed and on the user's PATH. All commands support `--namespace`, `--context`,
`--kubeconfig`, `--output json` (machine-readable), and `--verbose` flags.

### Argument Conventions

All commands accept a **bare pod name** (`my-pod`) or `pod/my-pod` — both work everywhere.
The `inspect` command also supports other types: `deployment/name`, `daemonset/name`, etc.
A bare name always defaults to pod.

### Quick Reference

| Command | Purpose | Example |
|---------|---------|---------|
| `kdiag health` | Cluster-wide health overview | `kdiag health -o json` |
| `kdiag diagnose <pod>` | Run all checks against a pod (inspect, refs, dns, netpol, ingress, EKS) | `kdiag diagnose my-pod -n prod` |
| `kdiag inspect <name>` | Deep-dive into a resource (defaults to pod) | `kdiag inspect my-pod` |
| `kdiag inspect <type/name>` | Deep-dive into a specific resource type | `kdiag inspect deployment/my-app` |
| `kdiag dns <pod-or-service>` | DNS resolution + CoreDNS health | `kdiag dns my-service` |
| `kdiag connectivity <src> <dst>` | Test network connectivity | `kdiag connectivity pod-a svc-b -p 80` |
| `kdiag trace <src-pod> <dst-svc>` | Map the full network path | `kdiag trace pod-a my-service` |
| `kdiag netpol <pod>` | Show NetworkPolicies affecting a pod | `kdiag netpol my-pod` |
| `kdiag ingress <name>` | Inspect Ingress rules, backends, TLS, controller health | `kdiag ingress my-ingress -n prod` |
| `kdiag logs <pod>` | Tail logs from a single pod | `kdiag logs my-pod` |
| `kdiag logs deployment/<name>` | Tail logs from all pods in a deployment | `kdiag logs deployment/my-app` |
| `kdiag logs -l <selector>` | Tail logs from matching pods | `kdiag logs -l app=myapp` |
| `kdiag shell <pod>` | Debug shell in a pod | `kdiag shell my-pod` |
| `kdiag shell --node <node>` | Debug shell on a node | `kdiag shell --node ip-10-0-1-5` |
| `kdiag capture <pod>` | Capture network traffic | `kdiag capture my-pod -c 100` |
| `kdiag eks cni` | EKS VPC CNI health | `kdiag eks cni` |
| `kdiag eks sg <pod>` | Security groups for a pod | `kdiag eks sg my-pod` |
| `kdiag eks node` | Node ENI/IP capacity | `kdiag eks node` |
| `kdiag eks node --show-pods` | Pods per node (daemonset vs workload) | `kdiag eks node --show-pods` |
| `kdiag eks endpoint` | Check VPC endpoints for AWS services | `kdiag eks endpoint` |

### Diagnose Checks

The `diagnose` command runs these checks automatically and reports pass/warn/fail for each:

| Check | What it verifies |
|-------|-----------------|
| `inspect` | Pod status, container states, restart counts, events |
| `refs` | ConfigMap and Secret references exist (missing = fail, optional missing = warn) |
| `dns` | CoreDNS pod health |
| `netpol` | NetworkPolicies affecting the pod |
| `ingress` | Ingress rules routing to this pod's services |
| `cni` | aws-node DaemonSet health + IP exhaustion (EKS only) |
| `sg` | Security groups attached to pod's ENI (EKS only) |

## Diagnostic Scripts

For common multi-step workflows, use the bundled scripts instead of running commands manually.
These scripts run the right commands in the right order and handle errors gracefully.

### Pod Triage
When a pod is failing (crashing, pending, erroring), run the full triage:
```bash
bash <skill-path>/scripts/pod-triage.sh <pod-name> -n <namespace>
```
This runs: diagnose → inspect → logs (if restarts detected). On EKS, add `--profile <name>`.

### Connectivity Check
When a service is unreachable from a pod:
```bash
bash <skill-path>/scripts/connectivity-check.sh <source-pod> <service> -n <namespace> -p <port>
```
This runs: trace → connectivity → dns → netpol on the source pod.

### EKS Health
For a comprehensive EKS cluster health check:
```bash
bash <skill-path>/scripts/eks-health.sh --profile <name>
```
This runs: health → eks cni → eks node → eks endpoint.

## Troubleshooting Playbooks

Follow these decision trees based on the symptom. Use `-o json` when you need to parse
output programmatically, and default table format when showing results to the user.

### Pod Not Running (CrashLoopBackOff, Pending, Failed, etc.)

Run the pod triage script, or manually:

1. Run `kdiag diagnose <pod>` to get a quick pass/warn/fail overview
2. Run `kdiag inspect <pod>` to see container states, restart counts, conditions, and events
3. Based on findings:
   - **CrashLoopBackOff**: Check logs with `kdiag logs <pod>` or `kdiag logs -l <selector>`
   - **Pending**: Look at events for scheduling failures (insufficient resources, node affinity, taints)
   - **ImagePullBackOff**: Check the image name and pull secrets in the events
   - **OOMKilled**: Container terminated reason will show OOMKilled - suggest increasing memory limits
4. If the pod is owned by a Deployment/StatefulSet/DaemonSet, also inspect the controller:
   `kdiag inspect deployment/<name>` to check replica status and rollout conditions
5. If diagnose shows `refs: fail`, a ConfigMap or Secret referenced by the pod doesn't exist — check the name and namespace

### Service Connectivity Issues

Run the connectivity check script, or manually:

1. Run `kdiag trace <source-pod> <service>` to map the full network path and verify endpoints exist
2. Run `kdiag connectivity <source-pod> <service>` to test actual TCP/HTTP connectivity
3. If connectivity fails:
   - Run `kdiag dns <service>` to verify DNS resolution and CoreDNS health
   - Run `kdiag netpol <pod>` on both source and destination pods to check for blocking NetworkPolicies
   - On EKS: run `kdiag eks sg <pod>` to check security group rules
4. If no endpoints: inspect the service selector labels vs. pod labels

### DNS Problems

1. Run `kdiag dns <service-or-pod>` to test resolution and check CoreDNS pod health
2. Look at:
   - Are CoreDNS pods Running and Ready?
   - Did the dig query resolve to IPs?
   - Is the query time unusually high (>100ms suggests issues)?
3. If CoreDNS is unhealthy, check with `kdiag inspect <coredns-pod> -n kube-system`

### Ingress Issues

1. Run `kdiag ingress <name>` to check:
   - Ingress class and controller type (ALB, NGINX, etc.)
   - Backend services exist and have ready endpoints
   - TLS secrets exist
   - Controller pod health
2. If backends show 0 ready endpoints: check that the Service selector matches pod labels
3. If TLS secret is missing: verify the secret name and namespace
4. For ALB issues: check aws-load-balancer-controller pods in kube-system
5. For NGINX issues: check ingress-nginx pods in ingress-nginx namespace

### Cluster-Wide Health Check

1. Run `kdiag health` for a full overview:
   - Node health (NotReady, memory/disk/PID pressure)
   - Pods with issues across all namespaces
   - Controller health (degraded Deployments, DaemonSets, StatefulSets)
   - Recent warning events
2. For any issues found, drill down with `kdiag inspect` or `kdiag diagnose`

### EKS-Specific Issues

These commands require the cluster to be EKS and valid AWS credentials.
Use `--profile` and `--region` flags if the default AWS credentials don't match the cluster.

Run the EKS health script for a full check, or individual commands:

1. **VPC CNI issues** (pod IP assignment failures): `kdiag eks cni`
   - Checks aws-node DaemonSet health
   - Reports nodes with exhausted IP capacity
2. **Security group problems**: `kdiag eks sg <pod>`
   - Shows security groups attached to the pod's ENI
3. **Node capacity**: `kdiag eks node`
   - Shows ENI and IP utilization per node
   - Flags nodes at >85% IP utilization
4. **What's consuming IPs on exhausted nodes**: `kdiag eks node --show-pods --status EXHAUSTED`
   - Lists every pod on exhausted nodes, separated into daemonset vs workload
   - Shows namespace breakdown so you can spot which namespaces dominate
   - Key insight: if all pods are daemonsets and zero are workloads, the node type is too small

### VPC Endpoint Issues

1. Run `kdiag eks endpoint` to check if AWS service traffic uses VPC endpoints
2. Services resolving to **private** IPs have VPC endpoints — traffic stays in your VPC
3. Services resolving to **public** IPs traverse the internet — consider adding VPC endpoints for security and performance
4. EKS API Access shows whether the cluster API server resolves to private or public IPs

### Network Debugging (Advanced)

For deeper network issues:

1. `kdiag capture <pod> -c 50` - Capture 50 packets from the pod's network namespace
2. `kdiag capture <pod> -f "port 80" -d 30s` - Capture HTTP traffic for 30 seconds
3. `kdiag capture <pod> -w capture.pcap` - Save to pcap file for Wireshark analysis
4. `kdiag shell <pod>` - Get an interactive debug shell with netshoot tools

## Interpreting Results

### Diagnose Severity Levels
- **pass**: Check passed, no issues
- **warn**: Potential issue, may need attention
- **fail**: Definite problem, needs fixing
- **error**: Check itself failed (permissions, API errors)
- **skipped**: Check not applicable (e.g., EKS checks on non-EKS cluster)

### Health Report
- **Critical**: At least one node NotReady, pod Failed/CrashLoopBackOff, or controller Unavailable
- **OK**: All checks passed

## Communication Style

- Lead with what you found and what it means, not a list of commands you're about to run
- After running a command, explain the output in plain language before suggesting next steps
- If something looks normal, say so and move on rather than elaborating
- When you find the root cause, give a clear, actionable fix
- Don't dump raw command output without interpretation
