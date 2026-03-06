# kdiag

Kubernetes diagnostics CLI with first-class EKS support.

`kdiag` consolidates network debugging, shell access, packet capture, DNS diagnostics, log aggregation, and AWS-specific checks into a single binary — replacing a toolbox of one-off scripts.

## Features

- **diagnose** — meta-command that runs all checks against a pod and surfaces severity-ranked results
- **inspect** — pod/container status, restart counts, and state details
- **dns** — CoreDNS health, pod DNS resolution, and lookup testing
- **netpol** — NetworkPolicy matching and reachability analysis
- **connectivity** — TCP/UDP reachability probes between pods or to external endpoints
- **health** — cluster-level health checks (nodes, system pods)
- **eks cni** — AWS VPC CNI DaemonSet health and per-node IP exhaustion check
- **eks sg** — Security group inspection for EKS nodes and pods
- **eks node** — EKS node metadata, instance types, and availability zone details
- **shell** — Drop an ephemeral debug container into a running pod
- **capture** — Live packet capture via tcpdump in an ephemeral container
- **logs** — Multi-container log aggregation with filtering
- **trace** — Network trace and latency diagnostics
- **completion** — Shell completion for bash, zsh, fish, and PowerShell

## Installation

### Homebrew

```sh
brew install lgbarn/kdiag/kdiag
```

### krew

Install from a local manifest (pre-release):

```sh
kubectl krew install --manifest=.krew.yaml
```

Install from the krew index (once available):

```sh
kubectl krew install kdiag
```

### Binary (curl)

```sh
# macOS arm64
curl -L https://github.com/lgbarn/kdiag/releases/download/v0.1.0/kdiag_0.1.0_darwin_arm64.tar.gz | tar xz
chmod +x kdiag
sudo mv kdiag /usr/local/bin/

# Linux amd64
curl -L https://github.com/lgbarn/kdiag/releases/download/v0.1.0/kdiag_0.1.0_linux_amd64.tar.gz | tar xz
chmod +x kdiag
sudo mv kdiag /usr/local/bin/
```

### kubectl plugin

Expose `kdiag` as `kubectl kdiag`:

```sh
ln -sf $(which kdiag) $(dirname $(which kdiag))/kubectl-kdiag
```

## Quick Start

Run all diagnostics against a pod:

```sh
kdiag diagnose my-pod -n my-namespace
```

Example output:

```
Pod: my-pod  Namespace: my-namespace  EKS: true

CHECK              SEVERITY  SUMMARY
─────────────────────────────────────────────────────────────────
inspect            pass      all 2 container(s) running normally
dns                warn      1/2 CoreDNS pod(s) not ready
netpol             pass      3 NetworkPolicy/ies matched
eks-cni            pass      DaemonSet healthy; 0 node(s) with exhausted IPs
eks-sg             pass      4 security groups retrieved

Total: 5  Pass: 4  Warn: 1  Fail: 0  Error: 0  Skipped: 0
```

## Command Reference

### diagnose

Run all diagnostic checks against a pod and display a severity-ranked summary.

```sh
kdiag diagnose <pod> [-n namespace]
```

Runs: inspect, dns, netpol, eks-cni, eks-sg (when on EKS).

---

### inspect

Show pod and container status including state, restart counts, and last state detail.

```sh
kdiag inspect <pod> [-n namespace] [-o table|json]
```

---

### dns

Check CoreDNS pod health and test DNS resolution from inside a pod.

```sh
kdiag dns <pod> [-n namespace] [--query hostname]
```

---

### netpol

List NetworkPolicies that select a pod and summarize ingress/egress rules.

```sh
kdiag netpol <pod> [-n namespace]
```

---

### connectivity

Probe TCP/UDP reachability from a pod to a target host and port.

```sh
kdiag connectivity <pod> <host> <port> [-n namespace] [--protocol tcp|udp]
```

---

### health

Check cluster-level health: node readiness, system pod status, and resource pressure.

```sh
kdiag health [-n kube-system]
```

---

### eks cni

Inspect AWS VPC CNI DaemonSet health and per-node IP address exhaustion.

```sh
kdiag eks cni [-n kube-system]
```

Requires: `ec2:DescribeInstances`, `ec2:DescribeNetworkInterfaces`.

---

### eks sg

Retrieve and display security groups attached to EKS nodes or pods.

```sh
kdiag eks sg [node|pod] <name> [-n namespace]
```

Requires: `ec2:DescribeSecurityGroups`.

---

### eks node

Show EKS node metadata: instance type, availability zone, AMI, and capacity type.

```sh
kdiag eks node [node-name]
```

Requires: `ec2:DescribeInstances`, `ec2:DescribeInstanceTypes`.

---

### shell

Launch an ephemeral debug container in a running pod.

```sh
kdiag shell <pod> [-n namespace] [--image nicolaka/netshoot]
```

Requires RBAC: `pods/ephemeralcontainers` create.

---

### capture

Capture network traffic from a pod via an ephemeral debug container.

By default, live output uses tshark with `-T ek` format (JSON-lines, one JSON
object per packet) which is optimized for consumption by AI agents and log
pipelines. Use `--format=text` for classic tcpdump output, or `--format=json`
for a tshark JSON array. When `--write` is used, output is always raw pcap.

```sh
# AI-friendly JSON-lines (default)
kdiag capture <pod> [-n namespace] [--filter "port 80"]

# Classic tcpdump text
kdiag capture <pod> --format text

# Write pcap file
kdiag capture <pod> -w /tmp/out.pcap

# Stop after 100 packets or 30 seconds
kdiag capture <pod> -c 100 -d 30s
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--filter` | `-f` | | BPF filter expression |
| `--write` | `-w` | | Write raw pcap to file |
| `--format` | | `ek` | Live output: `ek` (JSON-lines), `json`, `text` |
| `--interface` | `-i` | `any` | Network interface to capture on |
| `--count` | `-c` | `0` | Stop after N packets (0 = unlimited) |
| `--duration` | `-d` | `0` | Stop after duration (0 = unlimited) |

Requires RBAC: `pods/ephemeralcontainers` create.

---

### logs

Aggregate logs across all containers in a pod (or across pods in a deployment).

```sh
kdiag logs <pod|deployment/name> [-n namespace] [--tail 100] [--since 5m]
```

---

### trace

Run a network trace to diagnose latency and routing between a pod and a target.

```sh
kdiag trace <pod> <target> [-n namespace]
```

---

### completion

Generate shell completion script.

```sh
kdiag completion bash   # or zsh, fish, powershell
```

Add to your shell profile:

```sh
# bash
source <(kdiag completion bash)

# zsh
source <(kdiag completion zsh)
```

## Global Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--kubeconfig` | | `$KUBECONFIG` | Path to kubeconfig file |
| `--context` | | current context | Kubernetes context to use |
| `--namespace` | `-n` | default | Target namespace |
| `--output` | `-o` | `table` | Output format: `table` or `json` |
| `--image` | | `nicolaka/netshoot` | Debug container image |
| `--image-pull-secret` | | | Pull secret for private registry images |
| `--timeout` | | `30s` | Operation timeout (e.g. `30s`, `2m`) |
| `--verbose` | `-v` | false | Enable debug logging |

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

## IAM Requirements (EKS)

The following IAM permissions are required for `eks` subcommands:

- `ec2:DescribeInstances`
- `ec2:DescribeNetworkInterfaces`
- `ec2:DescribeSecurityGroups`
- `ec2:DescribeInstanceTypes`

These permissions should be granted to the IAM role associated with the node or the principal running `kdiag` (e.g., via IRSA or instance profile).

## License

MIT — see [LICENSE](LICENSE) for details.
