# kdiag eks node

Show per-node ENI and IP capacity: instance type limits versus current allocation.

## Synopsis

```
kdiag eks node [flags]
```

## Description

`kdiag eks node` lists all cluster nodes and for each managed EC2 node queries:

1. The instance type's maximum ENI count and IPs-per-ENI (via `DescribeInstanceTypes`)
2. The currently attached ENIs and their private IP counts (via `DescribeNetworkInterfaces`)

It then calculates utilization and flags nodes approaching or exceeding capacity.

**Status thresholds:**

| Utilization | Status |
|-------------|--------|
| < 70% | `OK` |
| 70–84% | `WARNING` |
| >= 85% | `EXHAUSTED` |

**Compute type awareness:**

- **Fargate nodes** — skipped entirely (no EC2 ENIs to query)
- **EKS Auto Mode nodes** — included with a note that ENI management is AWS-managed and limits may differ from standard node groups
- **Managed EC2 nodes** — fully evaluated

Nodes without a `node.kubernetes.io/instance-type` label or a parseable `spec.providerID` are skipped and listed in a separate table.

Unlike `kdiag eks cni`, this command does not read the aws-node DaemonSet or apply prefix delegation multipliers. It reports raw ENI/IP counts as seen by EC2.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | — | AWS shared config profile to use |
| `--region` | auto-detected | AWS region (parsed from the EKS API server endpoint when omitted) |

All [global flags](../README.md#global-flags) also apply (`--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Show ENI capacity for all nodes:**

```bash
kdiag eks node
```

**Get JSON output for monitoring integration:**

```bash
kdiag eks node -o json
```

**Use a specific AWS profile and region:**

```bash
kdiag eks node --profile prod --region us-east-1
```

**Show verbose output including per-node warnings:**

```bash
kdiag eks node -v
```

## Output

**Table output (default):**

```
NODE                                         INSTANCE_TYPE   COMPUTE    MAX_ENIS   MAX_IPS/ENI   ENIS   IPS   MAX_IPS   UTIL%   STATUS
ip-10-0-1-42.us-east-1.compute.internal      m5.large        managed    3          10            2      8     30        26%     OK
ip-10-0-1-55.us-east-1.compute.internal      m5.large        managed    3          10            3      25    30        83%     WARNING
ip-10-0-2-10.us-east-1.compute.internal      m5.xlarge       auto-mode  4          15            2      6     60        10%     OK

=== Skipped Nodes ===
NODE                                REASON
fargate-ip-10-0-3-1.internal        Fargate node — no EC2 ENIs

3 nodes checked, 1 skipped, 1 at risk
```

**JSON output (`-o json`):**

```json
{
  "nodes": [
    {
      "node_name": "ip-10-0-1-42.us-east-1.compute.internal",
      "instance_type": "m5.large",
      "compute_type": "managed",
      "max_enis": 3,
      "max_ips_per_eni": 10,
      "current_enis": 2,
      "current_ips": 8,
      "max_total_ips": 30,
      "utilization_pct": "26",
      "status": "OK",
      "note": ""
    },
    {
      "node_name": "ip-10-0-2-10.us-east-1.compute.internal",
      "instance_type": "m5.xlarge",
      "compute_type": "auto-mode",
      "max_enis": 4,
      "max_ips_per_eni": 15,
      "current_enis": 2,
      "current_ips": 6,
      "max_total_ips": 60,
      "utilization_pct": "10",
      "status": "OK",
      "note": "EKS Auto Mode — ENI management is AWS-managed; limits may differ"
    }
  ],
  "skipped_nodes": [
    {
      "node_name": "fargate-ip-10-0-3-1.internal",
      "reason": "Fargate node — no EC2 ENIs"
    }
  ],
  "summary": {
    "total_nodes": 4,
    "checked_nodes": 3,
    "skipped_nodes": 1,
    "exhausted_nodes": 0
  }
}
```

The `note` field is omitted from JSON when empty.

## Required Permissions

**Kubernetes RBAC:**

| Resource | Verbs |
|----------|-------|
| `nodes` | `list` |

**IAM:**

| Action | Purpose |
|--------|---------|
| `ec2:DescribeInstanceTypes` | Fetch ENI and IP-per-ENI limits (batched by unique instance types across all nodes) |
| `ec2:DescribeNetworkInterfaces` | Count attached ENIs and private IPs per node |

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

## Relationship to `kdiag eks cni`

`kdiag eks cni` and `kdiag eks node` both report node ENI/IP capacity, but serve different purposes:

| | `eks cni` | `eks node` |
|-|-----------|------------|
| Shows aws-node DaemonSet health | Yes | No |
| Shows CNI env-var configuration | Yes | No |
| Applies prefix delegation multiplier | Yes | No |
| Shows compute type (managed/auto-mode) | No | Yes |
| `WARNING` threshold at 70% | No | Yes |
| Summary includes `exhausted_nodes` count | Yes (JSON) | Yes (JSON) |

Use `eks cni` when diagnosing VPC CNI configuration issues or IP pool exhaustion in the context of the CNI's warm pool settings. Use `eks node` for a straightforward capacity audit across all node types including EKS Auto Mode.

## Troubleshooting

**"instance type limits not available for \<type\>"**

`DescribeInstanceTypes` returned no network info for this instance type. This can occur with very new or unusual instance families. The node is skipped; check the AWS EC2 documentation for the instance type's network limits manually.

**Node shows WARNING or EXHAUSTED but pod scheduling is not failing**

IP pressure at these thresholds is a leading indicator. Pods will begin failing to schedule once all available IPs are assigned. Consider adding nodes, switching to a larger instance type, or enabling prefix delegation.

**All nodes are skipped**

Verify that your nodes have `spec.providerID` set (standard for EKS managed node groups) and carry the `node.kubernetes.io/instance-type` label. Self-managed nodes or nodes joined without the AWS cloud provider may lack these fields.
