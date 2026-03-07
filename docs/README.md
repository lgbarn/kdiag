# kdiag

A Kubernetes diagnostic CLI for EKS clusters. Drop into a debug shell, capture packets, and diagnose network issues without juggling kubectl, ksniff, and netshoot separately.

## Available Commands

| Command | What it does |
|---------|-------------|
| `kdiag shell <pod>` | Interactive debug shell inside a pod via ephemeral container |
| `kdiag shell --node <node>` | Privileged debug shell on an EC2 node |
| `kdiag capture <pod>` | Live tcpdump output from a pod; save to .pcap for Wireshark |
| `kdiag dns <pod-or-service>` | Test DNS resolution from inside a pod; check CoreDNS health |
| `kdiag connectivity <pod> <dest>` | Test TCP or HTTP connectivity from a pod to a service, pod, or host:port |
| `kdiag trace <pod> <service>` | Map the network path from a pod through a service to its endpoints |
| `kdiag netpol <pod>` | List NetworkPolicies that apply to a pod and summarize their rules |
| `kdiag logs -l <selector>` | Tail logs from pods matching a label selector with color-coded output |
| `kdiag inspect <type/name>` | Show ownership chain, conditions, container status, and events for a resource |
| `kdiag health` | Cluster-wide health report across nodes, pods, controllers, and events |
| `kdiag eks cni` | VPC CNI health: aws-node DaemonSet status, CNI configuration, and per-node IP capacity |
| `kdiag eks sg <pod>` | Effective security groups for a pod (branch ENI for SGP pods, node primary ENI otherwise) |
| `kdiag eks node` | Per-node ENI and IP capacity: instance type limits vs current allocation, with WARNING/EXHAUSTED thresholds |

## Prerequisites

- Go 1.23+
- A kubeconfig with access to your EKS cluster
- RBAC permissions — see [RBAC requirements](#rbac-requirements) below
- AWS credentials (for `eks` subcommands) — see [IAM requirements](#iam-requirements) below

## Installation

```bash
go install github.com/lgbarn/kdiag@latest
```

Or build from source:

```bash
git clone https://github.com/lgbarn/kdiag
cd kdiag
go build -o kdiag .
```

kdiag can also run as a kubectl plugin. Copy or symlink the binary as `kubectl-kdiag` anywhere on your `PATH`:

```bash
cp kdiag /usr/local/bin/kubectl-kdiag
kubectl kdiag shell my-pod
```

## Quick Start

**Debug shell into a pod:**

```bash
kdiag shell my-pod
kdiag shell my-pod -n kube-system
```

**Debug shell on a node:**

```bash
kdiag shell --node ip-10-0-1-42.us-east-1.compute.internal
```

**Capture packets from a pod (live output):**

```bash
kdiag capture my-pod
kdiag capture my-pod --filter "port 443"
```

**Save a capture to a .pcap file:**

```bash
kdiag capture my-pod -w /tmp/trace.pcap --duration 30s
```

**Test DNS resolution from inside a pod:**

```bash
kdiag dns my-service
kdiag dns my-pod -n production
```

**Test TCP or HTTP connectivity between pods:**

```bash
kdiag connectivity my-pod my-service
kdiag connectivity my-pod other-pod --port 8080
kdiag connectivity my-pod 10.0.1.42:5432
```

**Map the network path to a service:**

```bash
kdiag trace my-pod my-service
kdiag trace my-pod my-service -o json
```

**Inspect NetworkPolicies for a pod:**

```bash
kdiag netpol my-pod
kdiag netpol my-pod -n production
```

**Tail logs from pods matching a label selector:**

```bash
kdiag logs -l app=myapp
kdiag logs -l app=myapp --filter error --max-pods 5
kdiag logs -l app=myapp -o json
```

**Inspect a resource — ownership chain, conditions, events:**

```bash
kdiag inspect pod/myapp-6d9f4b-xkp2j
kdiag inspect deployment/myapp -n production
kdiag inspect deployment/myapp -o json
```

**Cluster-wide health check (exits 1 on critical issues):**

```bash
kdiag health
kdiag health -o json
kdiag health || alert "cluster degraded"
```

**Check VPC CNI health and node IP capacity:**

```bash
kdiag eks cni
kdiag eks cni -o json
```

**Show security groups for a pod:**

```bash
kdiag eks sg my-pod
kdiag eks sg my-pod -n production -o json
```

**Show per-node ENI capacity:**

```bash
kdiag eks node
kdiag eks node --profile staging -o json
```

## Global Flags

These flags apply to every command.

| Flag | Default | Description |
|------|---------|-------------|
| `--kubeconfig` | `~/.kube/config` | Path to kubeconfig file |
| `--context` | current context | Kubeconfig context to use |
| `-n`, `--namespace` | context default | Target namespace |
| `-o`, `--output` | `table` | Output format: `table` or `json` |
| `--image` | `nicolaka/netshoot` | Debug container image |
| `--image-pull-secret` | — | Pull secret for private registries |
| `--timeout` | `30s` | Timeout for container start operations |
| `-v`, `--verbose` | false | Print diagnostic steps to stderr |

The following flags apply only to `eks` subcommands.

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | — | AWS shared config profile (`~/.aws/config`) |
| `--region` | auto-detected | AWS region; auto-detected from the EKS API server endpoint when omitted |

## RBAC Requirements

`kdiag shell` and `kdiag capture` both inject ephemeral containers. The standard `admin` ClusterRole does **not** include the `pods/ephemeralcontainers` subresource (see [kubernetes#120909](https://github.com/kubernetes/kubernetes/issues/120909)).

kdiag checks permissions before attempting any operation and prints a remediation message if permissions are missing:

```
Missing permissions:
  - update pods/ephemeralcontainers
  - create pods/attach

remediation: to grant ephemeral container permissions, ask your cluster admin to add these rules:
  - verbs: ["update"], resources: ["pods/ephemeralcontainers"]
  - verbs: ["create"], resources: ["pods/attach"]
```

Example ClusterRole addition:

```yaml
rules:
  - apiGroups: [""]
    resources: ["pods/ephemeralcontainers"]
    verbs: ["update"]
  - apiGroups: [""]
    resources: ["pods/attach"]
    verbs: ["create"]
```

`kdiag shell --node` requires `pods/create` in the target namespace instead of ephemeral container permissions.

## IAM Requirements

The `eks` subcommands call EC2 APIs and require valid AWS credentials. Credentials are loaded in the standard AWS SDK order: environment variables, `~/.aws/credentials`, IAM instance profile, IRSA.

Minimum IAM policy for all `eks` commands:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeSecurityGroups"
      ],
      "Resource": "*"
    }
  ]
}
```

`ec2:DescribeSecurityGroups` is only required by `kdiag eks sg`. `kdiag eks cni` and `kdiag eks node` need only `ec2:DescribeInstanceTypes` and `ec2:DescribeNetworkInterfaces`.

## Debug Image

The default debug image is `nicolaka/netshoot`, which includes tcpdump, dig, curl, nmap, iperf3, and other network tools.

For air-gapped or private registry environments:

```bash
kdiag shell my-pod --image my-registry.internal/netshoot:latest --image-pull-secret regcred
```

## EKS and Fargate

kdiag detects whether a pod is running on Fargate by checking the `eks.amazonaws.com/fargate-profile` label and the `fargate-ip-` node name prefix. It also detects EKS Auto Mode nodes via the `eks.amazonaws.com/compute-type=auto` label.

- **Fargate pods**: `kdiag shell <pod>` emits a warning before attempting the ephemeral container injection, since Fargate may not support ephemeral containers. `kdiag eks sg` and `kdiag eks cni` skip Fargate pods/nodes entirely.
- **Fargate nodes**: `kdiag shell --node <node>` exits with an error immediately — Fargate virtual nodes do not support privileged pods or host namespace access.
- **EKS Auto Mode nodes**: included in `kdiag eks node` output with a note that ENI management is AWS-managed.

## Command Reference

- [shell](commands/shell.md) — debug shell into pods and nodes
- [capture](commands/capture.md) — packet capture via tcpdump
- [dns](commands/dns.md) — DNS resolution and CoreDNS health check
- [connectivity](commands/connectivity.md) — TCP/HTTP connectivity testing between pods
- [trace](commands/trace.md) — network path mapping from pod to service endpoints
- [netpol](commands/netpol.md) — NetworkPolicy inspection for a pod
- [logs](commands/logs.md) — multi-pod log tailing with color-coded output
- [inspect](commands/inspect.md) — enriched resource details: ownership chain, conditions, events
- [health](commands/health.md) — cluster-wide health report with CI-compatible exit codes
- [eks cni](commands/eks-cni.md) — VPC CNI health and per-node IP capacity
- [eks sg](commands/eks-sg.md) — effective security groups for a pod
- [eks node](commands/eks-node.md) — per-node ENI and IP capacity with WARNING/EXHAUSTED thresholds
