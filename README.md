# kdiag

**Stop guessing why your pods are broken.** Just describe the problem — kdiag figures out the rest.

kdiag is a Kubernetes diagnostics CLI that pairs with [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to give you an AI-powered troubleshooting partner. Instead of juggling `kubectl describe`, `kubectl logs`, AWS console tabs, and Stack Overflow, you tell Claude what's wrong and it runs the right commands, interprets the results, and walks you through the fix.

## How It Works

Install kdiag and the Claude Code skill. Then just talk to Claude:

> **You:** "my pod keeps crashing in production"
>
> Claude runs `kdiag diagnose my-pod -n prod`, sees the container is OOMKilled, checks memory limits, and tells you exactly what to change.

> **You:** "pod-a can't reach the payment service"
>
> Claude traces the network path with `kdiag trace pod-a payment-svc`, checks DNS, tests connectivity, scans NetworkPolicies, and pinpoints that a netpol is blocking egress on port 443.

> **You:** "pods are stuck pending on our EKS cluster"
>
> Claude runs `kdiag eks cni`, finds 3 nodes with exhausted IP addresses, and recommends enabling prefix delegation.

No memorizing flags. No copy-pasting between terminals. You describe the symptom, Claude drives the investigation.

### What the Skill Gives You

The Claude Code skill isn't just a wrapper — it includes troubleshooting playbooks that Claude follows to systematically work through common failure patterns:

- **Pod failures** — CrashLoopBackOff, Pending, ImagePullBackOff, OOMKilled
- **Service connectivity** — DNS resolution, endpoint mapping, TCP reachability
- **Network policies** — which policies affect a pod, what traffic they block
- **EKS-specific issues** — VPC CNI IP exhaustion, security group rules, node capacity
- **Cluster health** — node pressure, degraded controllers, warning events

Each playbook knows which kdiag commands to run, in what order, and what the output means.

## Quick Start

### 1. Install kdiag

Download from the [Releases](https://github.com/lgbarn/kdiag/releases) page, or:

```sh
go install github.com/lgbarn/kdiag@latest
```

### 2. Install the Claude Code skill

```sh
cp skill/SKILL.md ~/.claude/skills/kdiag-SKILL.md
```

Or add it to your project's `.claude/settings.json`:

```json
{
  "skills": ["./skill/SKILL.md"]
}
```

### 3. Start troubleshooting

Open Claude Code and describe your problem. The skill activates automatically when you mention Kubernetes issues:

- "my pod is stuck in CrashLoopBackOff"
- "I can't reach my-service from pod-a"
- "check the health of my cluster"
- "why is my pod pending?"
- "are there any network policies blocking traffic?"

## Using kdiag Directly

You don't need Claude Code to use kdiag — every command works standalone from the terminal.

### One command to check everything

```sh
kdiag diagnose my-pod -n my-namespace
```

```
Diagnosing pod: my-namespace/my-pod
EKS cluster: yes

CHECK     SEVERITY   SUMMARY
inspect   pass       all 2 container(s) running normally
dns       warn       1/2 CoreDNS pod(s) not ready
netpol    pass       3 NetworkPolicy/ies matched
cni       pass       DaemonSet healthy; 0 node(s) with exhausted IPs
sg        pass       4 security groups retrieved

Summary: 5 total, 4 pass, 1 warn, 0 fail, 0 error, 0 skipped
```

### All commands

| Command | What it does |
|---------|-------------|
| `kdiag diagnose <pod>` | Run all checks at once and get a severity-ranked summary |
| `kdiag inspect <pod>` | Container states, restart counts, events, owner chain |
| `kdiag inspect deployment/<name>` | Deployment replica status and rollout conditions |
| `kdiag health` | Cluster-wide node and system pod health |
| `kdiag dns <pod-or-service>` | DNS resolution test + CoreDNS health check |
| `kdiag connectivity <src> <dst>` | TCP/HTTP reachability from one pod to another |
| `kdiag trace <pod> <service>` | Map the full network path: pod → service → endpoints → nodes |
| `kdiag netpol <pod>` | Which NetworkPolicies affect this pod and what they allow/block |
| `kdiag logs <pod>` | Tail logs from a pod |
| `kdiag logs deployment/<name>` | Tail logs from all pods in a deployment |
| `kdiag logs -l app=myapp` | Tail logs by label selector |
| `kdiag shell <pod>` | Drop a debug shell (netshoot) into a running pod |
| `kdiag capture <pod>` | Live packet capture with JSON-lines, text, or pcap output |
| `kdiag eks cni` | VPC CNI DaemonSet health + per-node IP exhaustion |
| `kdiag eks sg <pod>` | Security groups attached to a pod's ENI |
| `kdiag eks node` | Node metadata: instance type, AZ, ENI/IP capacity |
| `kdiag eks node --show-pods` | Same + list pods per node (daemonset vs workload breakdown) |
| `kdiag eks node --show-pods --status EXHAUSTED` | Only show pods on exhausted nodes |

All commands accept bare pod names (`my-pod`) or type/name format (`pod/my-pod`) — both work everywhere.

Every command supports `--output json` for machine-readable output, `--namespace`, `--context`, and `--kubeconfig` flags.

## Installation

### Binary (GitHub Release)

Download from the [Releases](https://github.com/lgbarn/kdiag/releases) page, or use curl (replace `VERSION` with the release version, e.g. `0.1.0`):

```sh
# macOS arm64
curl -L https://github.com/lgbarn/kdiag/releases/download/v${VERSION}/kdiag_${VERSION}_darwin_arm64.tar.gz | tar xz
sudo mv kdiag /usr/local/bin/

# macOS amd64
curl -L https://github.com/lgbarn/kdiag/releases/download/v${VERSION}/kdiag_${VERSION}_darwin_amd64.tar.gz | tar xz
sudo mv kdiag /usr/local/bin/

# Linux amd64
curl -L https://github.com/lgbarn/kdiag/releases/download/v${VERSION}/kdiag_${VERSION}_linux_amd64.tar.gz | tar xz
sudo mv kdiag /usr/local/bin/

# Linux arm64
curl -L https://github.com/lgbarn/kdiag/releases/download/v${VERSION}/kdiag_${VERSION}_linux_arm64.tar.gz | tar xz
sudo mv kdiag /usr/local/bin/
```

### Build from source

```sh
go install github.com/lgbarn/kdiag@latest
```

### kubectl plugin

Expose `kdiag` as `kubectl kdiag`:

```sh
ln -sf $(which kdiag) $(dirname $(which kdiag))/kubectl-kdiag
```

## Works With Any Kubernetes Cluster

kdiag uses your standard kubeconfig, so it works wherever `kubectl` works:

- **Docker Desktop** Kubernetes
- **kind** and **k3d** local clusters
- **Amazon EKS** (with bonus EKS-specific commands)
- **GKE**, **AKS**, and any conformant cluster

EKS commands (`eks cni`, `eks sg`, `eks node`) require AWS credentials and are automatically skipped on non-EKS clusters.

## RBAC Requirements

| Command | Verb | Resource |
|---------|------|----------|
| inspect | get | pods |
| dns | get, list | pods |
| netpol | list | networkpolicies |
| connectivity | create | pods/ephemeralcontainers |
| health | list | nodes, pods |
| eks cni | get, list | daemonsets, pods |
| eks sg | get | nodes, pods |
| eks node | list | nodes |
| shell | create | pods/ephemeralcontainers |
| capture | create | pods/ephemeralcontainers |
| logs | get | pods/log |
| trace | create | pods/ephemeralcontainers |
| diagnose | get, list | pods, networkpolicies, daemonsets |

## EKS IAM Requirements

The `eks` subcommands need these IAM permissions on the principal running kdiag:

- `ec2:DescribeInstances`
- `ec2:DescribeNetworkInterfaces`
- `ec2:DescribeSecurityGroups`
- `ec2:DescribeInstanceTypes`

## License

MIT — see [LICENSE](LICENSE) for details.
