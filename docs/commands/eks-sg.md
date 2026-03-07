# kdiag eks sg

Show the effective security groups for a pod — either from its branch ENI (Security Groups for Pods) or inherited from the node's primary ENI.

## Synopsis

```
kdiag eks sg <pod> [flags]
```

## Description

`kdiag eks sg` determines which ENI controls a pod's network traffic and fetches the full security group rules for that ENI from EC2.

**Two code paths:**

- **Security Groups for Pods** — if the pod has the `vpc.amazonaws.com/pod-eni` annotation (set by the VPC CNI when a `SecurityGroupPolicy` is in effect), the branch ENI's security groups are shown. The ENI ID is read from the annotation and used directly.

- **Node-inherited security groups** — all other pods inherit the security groups of the node they run on. kdiag resolves the node's EC2 instance ID from `spec.providerID`, queries the primary ENI (device index 0), and returns its security groups.

Fargate pods are rejected immediately — Fargate manages networking outside EC2 ENIs and is not compatible with this command.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | — | AWS shared config profile to use |
| `--region` | auto-detected | AWS region (parsed from the EKS API server endpoint when omitted) |

All [global flags](../README.md#global-flags) also apply (`--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Show security groups for a pod:**

```bash
kdiag eks sg my-pod
```

**Show security groups for a pod in a specific namespace:**

```bash
kdiag eks sg my-pod -n production
```

**Get JSON output for diffing or auditing:**

```bash
kdiag eks sg my-pod -o json
```

**Use a named AWS profile:**

```bash
kdiag eks sg my-pod --profile prod-readonly
```

## Output

**Table output (default):**

```
Pod:        production/my-pod
Node:       ip-10-0-1-42.us-east-1.compute.internal
ENI Source: node-primary-eni (inherited from node)

Security Group: sg-0a1b2c3d4e5f (my-cluster-node-sg)
  Description: EKS node group security group
  Ingress Rules:
    PROTOCOL   FROM    TO      SOURCE            DESCRIPTION
    tcp        443     443     10.0.0.0/8        cluster internal
    tcp        10250   10250   sg-0f9e8d7c6b5a   kubelet from control plane
  Egress Rules:
    PROTOCOL   FROM   TO   DESTINATION   DESCRIPTION
    all        *      *    0.0.0.0/0
```

For pods using Security Groups for Pods, an `ENI ID` line is added and `ENI Source` reads `branch-eni (security groups for pods)`.

All-traffic rules (protocol `all` or both port fields zero) display `*` in the FROM and TO columns.

**JSON output (`-o json`):**

```json
{
  "pod_name": "my-pod",
  "namespace": "production",
  "node_name": "ip-10-0-1-42.us-east-1.compute.internal",
  "eni_source": "node-primary-eni (inherited from node)",
  "eni_id": "",
  "security_groups": [
    {
      "group_id": "sg-0a1b2c3d4e5f",
      "group_name": "my-cluster-node-sg",
      "description": "EKS node group security group",
      "ingress_rules": [
        {
          "protocol": "tcp",
          "from_port": 443,
          "to_port": 443,
          "cidrs": ["10.0.0.0/8"],
          "source_groups": [],
          "description": "cluster internal"
        }
      ],
      "egress_rules": [
        {
          "protocol": "all",
          "from_port": 0,
          "to_port": 0,
          "cidrs": ["0.0.0.0/0"],
          "source_groups": [],
          "description": ""
        }
      ]
    }
  ]
}
```

For pods using Security Groups for Pods, `eni_id` is populated with the branch ENI ID.

## Required Permissions

**Kubernetes RBAC:**

| Resource | Verbs |
|----------|-------|
| `pods` | `get` |
| `nodes` | `get` (for node-inherited path only) |

**IAM:**

| Action | Purpose |
|--------|---------|
| `ec2:DescribeNetworkInterfaces` | Resolve security group IDs from the ENI |
| `ec2:DescribeSecurityGroups` | Fetch full rule details for each security group |

Minimum IAM policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeSecurityGroups"
      ],
      "Resource": "*"
    }
  ]
}
```

## Troubleshooting

**"pod appears to be a Fargate pod"**

Fargate networking is managed by AWS outside of EC2 ENIs. There is no EC2 ENI to query for Fargate pods.

**"failed to parse pod ENI annotation"**

The `vpc.amazonaws.com/pod-eni` annotation is present but contains invalid JSON. This may indicate a partial CNI initialization. Check the aws-node DaemonSet logs and the pod's annotation value directly with `kubectl get pod <pod> -o jsonpath='{.metadata.annotations.vpc\.amazonaws\.com/pod-eni}'`.

**"primary ENI for instance not found"**

The EC2 instance may have been terminated or the ENI detached. Verify the node is still running: `kubectl get node <node>`.

**Pod has Security Groups for Pods but only node SGs are shown**

The pod must have the `vpc.amazonaws.com/pod-eni` annotation applied by the CNI to be recognized. If a `SecurityGroupPolicy` exists but the annotation is absent, the CNI has not yet assigned a branch ENI — check aws-node pod logs for errors.
