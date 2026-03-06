# kdiag netpol

List the NetworkPolicies that apply to a pod and summarize their ingress and egress rules.

## Synopsis

```
kdiag netpol <pod-name> [flags]
```

## Description

`kdiag netpol` answers the question: "which NetworkPolicies actually affect this pod?" It fetches all NetworkPolicies in the namespace, evaluates each policy's `podSelector` against the pod's labels, and prints a human-readable summary of every matching policy's rules.

This is a pure read operation — no ephemeral containers are created, and no traffic is generated.

When no policies match, kdiag prints an informational message to stderr explaining that the pod's traffic is unrestricted by NetworkPolicies. This is distinct from a failure.

## Flags

No command-specific flags. All [global flags](../README.md#global-flags) apply (`--namespace`, `--output`, `--timeout`, etc.).

## Examples

**Check which NetworkPolicies apply to a pod:**

```bash
kdiag netpol my-pod
```

**Check in a specific namespace:**

```bash
kdiag netpol my-pod -n production
```

**Get JSON output:**

```bash
kdiag netpol my-pod -o json
```

**Verbose mode:**

```bash
kdiag netpol my-pod -v
# [kdiag] resolving pod "my-pod" in namespace "default"
# [kdiag] listing NetworkPolicies in namespace "default"
# [kdiag] found 3 NetworkPolicies; matching against pod labels
```

## Output

**Table output (default):**

```
Policy: allow-frontend-ingress
  Pod Selector: app=my-pod
  Policy Types: Ingress
  Ingress Rules:
    Rule 1:
      Ports: TCP/80, TCP/443
      From: pods: app=frontend

Policy: restrict-egress
  Pod Selector: app=my-pod
  Policy Types: Egress
  Egress Rules:
    Rule 1:
      Ports: UDP/53
      To: namespaces: kubernetes.io/metadata.name=kube-system
    Rule 2:
      Ports: TCP/5432
      To: pods: app=postgres
```

When no policies match:

```
No NetworkPolicies select pod "my-pod" in namespace "default".
This means the pod's traffic is not restricted by NetworkPolicies.
```

**JSON output (`-o json`):**

```json
{
  "pod": "my-pod",
  "policies": [
    {
      "name": "allow-frontend-ingress",
      "namespace": "default",
      "pod_selector": "app=my-pod",
      "policy_types": ["Ingress"],
      "ingress": [
        {
          "ports": ["TCP/80", "TCP/443"],
          "from": ["pods: app=frontend"]
        }
      ]
    }
  ]
}
```

An empty `policies` array in JSON output means no policies apply.

## How Policy Matching Works

kdiag uses `metav1.LabelSelectorAsSelector` and `labels.Set.Matches` — the same mechanism Kubernetes itself uses for NetworkPolicy enforcement. This means:

- An empty selector (`{}`) matches all pods in the namespace (not just pods with no labels).
- A nil selector behaves identically to an empty selector.
- `matchExpressions` are supported in addition to `matchLabels`.

## Rule Summary Format

Ports are formatted as `PROTOCOL/PORT` (e.g., `TCP/80`, `UDP/53`). An empty port list is shown as `<all ports>`.

Peers are described as:
- `pods: <selector>` — PodSelector peer
- `namespaces: <selector>` — NamespaceSelector peer
- `ipBlock: <CIDR> except [<CIDRs>]` — IPBlock peer
- `<all sources>` / `<all destinations>` — empty peer list (allow all)

Selectors are formatted as `key=value,...` pairs. `<all>` means nil selector; `<all pods>` means an empty (match-all) selector.

## RBAC Requirements

`kdiag netpol` requires:
- `get pods` in the target namespace
- `list networkpolicies` in the target namespace (`networking.k8s.io` API group)

These permissions are typically included in the `view` ClusterRole.

## Troubleshooting

**"pod X not found in namespace Y"**

Verify the pod name and namespace:

```bash
kubectl get pods -n production
kdiag netpol my-pod -n production
```

**Output shows no matching policies but traffic is being blocked**

NetworkPolicies are not the only traffic enforcement mechanism on EKS. Also check:
- AWS security groups on the nodes and ENIs
- VPC route tables
- AWS Network Firewall or third-party CNI policy enforcement (e.g., Calico GlobalNetworkPolicy)

**A policy appears that you didn't expect**

An empty `podSelector: {}` matches every pod in the namespace. Check for namespace-wide default-deny or allow policies:

```bash
kubectl get networkpolicies -n production -o yaml | grep -A5 podSelector
```
