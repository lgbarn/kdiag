# kdiag

A Kubernetes diagnostic CLI for EKS clusters. Drop into a debug shell, capture packets, and diagnose network issues without juggling kubectl, ksniff, and netshoot separately.

## What's in Phase 1

| Command | What it does |
|---------|-------------|
| `kdiag shell <pod>` | Interactive debug shell inside a pod via ephemeral container |
| `kdiag shell --node <node>` | Privileged debug shell on an EC2 node |
| `kdiag capture <pod>` | Live tcpdump output from a pod; save to .pcap for Wireshark |

> Phase 1 implements shell and capture. DNS, connectivity, trace, logs, EKS-specific diagnostics, and the `diagnose` meta-command are planned for later phases.

## Prerequisites

- Go 1.23+
- A kubeconfig with access to your EKS cluster
- RBAC permissions — see [RBAC requirements](#rbac-requirements) below

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

## Debug Image

The default debug image is `nicolaka/netshoot`, which includes tcpdump, dig, curl, nmap, iperf3, and other network tools.

For air-gapped or private registry environments:

```bash
kdiag shell my-pod --image my-registry.internal/netshoot:latest --image-pull-secret regcred
```

## EKS and Fargate

kdiag detects whether a pod is running on Fargate by checking the `eks.amazonaws.com/fargate-profile` label and the `fargate-ip-` node name prefix.

- **Fargate pods**: `kdiag shell <pod>` emits a warning before attempting the ephemeral container injection, since Fargate may not support ephemeral containers.
- **Fargate nodes**: `kdiag shell --node <node>` exits with an error immediately — Fargate virtual nodes do not support privileged pods or host namespace access.

## Command Reference

- [shell](commands/shell.md) — debug shell into pods and nodes
- [capture](commands/capture.md) — packet capture via tcpdump
