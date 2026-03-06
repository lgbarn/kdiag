# kdiag eks cni

Inspect the VPC CNI configuration and report per-node ENI/IP capacity against current usage.

## Synopsis

```
kdiag eks cni [flags]
```

## Description

`kdiag eks cni` reads the `aws-node` DaemonSet from `kube-system` and queries EC2 for ENI and IP capacity limits per instance type. It shows whether the CNI is healthy, which env-var tuning knobs are active, and how close each node is to IP exhaustion.

A node is flagged `EXHAUSTED` when current IP allocation reaches 85% or more of the theoretical maximum. With prefix delegation enabled (`ENABLE_PREFIX_DELEGATION=true`), each IP slot holds a /28 prefix (16 IPs), so the effective capacity is multiplied by 16.

Fargate nodes and nodes without a parseable `providerID` or `node.kubernetes.io/instance-type` label are skipped and listed in a separate table.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--aws-profile` | — | AWS shared config profile to use |
| `--aws-region` | auto-detected | AWS region (parsed from the EKS API server endpoint when omitted) |

All [global flags](../README.md#global-flags) also apply (`--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Check CNI health and node IP capacity:**

```bash
kdiag eks cni
```

**Use a specific AWS profile:**

```bash
kdiag eks cni --aws-profile staging
```

**Get structured JSON for scripting or CI:**

```bash
kdiag eks cni -o json
```

**Override the detected region:**

```bash
kdiag eks cni --aws-region us-west-2
```

**Show verbose output including skipped-node warnings:**

```bash
kdiag eks cni -v
```

## Output

**Table output (default) — three sections:**

```
=== aws-node DaemonSet Status ===
DESIRED   READY   UPDATED
3         3       3

=== VPC CNI Configuration ===
SETTING                      VALUE
ENABLE_PREFIX_DELEGATION     false
ENABLE_POD_ENI               false
WARM_IP_TARGET
WARM_ENI_TARGET              1
WARM_PREFIX_TARGET
MINIMUM_IP_TARGET

=== Node IP Capacity ===
NODE                                         INSTANCE_TYPE   MAX_ENIS   MAX_IPS/ENI   MAX_IPS   CURRENT_IPS   UTIL%   STATUS
ip-10-0-1-42.us-east-1.compute.internal      m5.large        3          10            30        12            40%     OK
ip-10-0-1-55.us-east-1.compute.internal      m5.large        3          10            30        26            86%     EXHAUSTED

2 nodes checked, 1 exhausted, 0 skipped
```

When prefix delegation is active the capacity section heading becomes `=== Node IP Capacity (prefix delegation: x16) ===` and `MAX_IPS` reflects the multiplied value.

Nodes that could not be evaluated appear in a `=== Skipped Nodes ===` table below the capacity section.

**JSON output (`-o json`):**

```json
{
  "daemonset": {
    "desired": 3,
    "ready": 3,
    "updated": 3,
    "healthy": true
  },
  "config": {
    "prefix_delegation": false,
    "pod_eni": false,
    "warm_ip_target": "",
    "warm_eni_target": "1",
    "warm_prefix_target": "",
    "minimum_ip_target": ""
  },
  "nodes": [
    {
      "node_name": "ip-10-0-1-42.us-east-1.compute.internal",
      "instance_type": "m5.large",
      "max_enis": 3,
      "max_ips_per_eni": 10,
      "max_total_ips": 30,
      "current_enis": 2,
      "current_ips": 12,
      "utilization_pct": "40",
      "exhausted": false
    }
  ],
  "skipped_nodes": [],
  "ip_exhausted_nodes": ["ip-10-0-1-55.us-east-1.compute.internal"]
}
```

## Required Permissions

**Kubernetes RBAC:**

| Resource | Verbs |
|----------|-------|
| `daemonsets` in `kube-system` | `get` |
| `nodes` | `list` |

**IAM:**

| Action | Purpose |
|--------|---------|
| `ec2:DescribeInstanceTypes` | Fetch ENI and IP-per-ENI limits for each instance type (batched by unique types) |
| `ec2:DescribeNetworkInterfaces` | Count current ENIs and private IPs per node |

Minimum IAM policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeNetworkInterfaces"
      ],
      "Resource": "*"
    }
  ]
}
```

## Troubleshooting

**"failed to get aws-node DaemonSet"**

The `aws-node` DaemonSet must exist in `kube-system`. If you are using a custom CNI plugin this command does not apply.

**"AWS credentials not found"**

Configure credentials with one of: `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` environment variables, `aws configure`, an IAM instance profile, or IRSA when running inside the cluster.

**Node shows EXHAUSTED but pods schedule fine**

IP exhaustion at 85% is a warning threshold, not a hard limit. However, as new pods are scheduled the node may start rejecting them with `Insufficient private IPv4 addresses`. Consider enabling prefix delegation or adding nodes.

**Node is skipped with "cannot parse providerID"**

This occurs on nodes whose `spec.providerID` is not in `aws:///<az>/<instance-id>` format — for example, custom or local nodes in a mixed cluster. These nodes do not have EC2 ENIs queryable by this command.
