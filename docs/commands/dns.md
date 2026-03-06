# kdiag dns

Test DNS resolution from inside a pod's network namespace and check CoreDNS pod health.

## Synopsis

```
kdiag dns <pod-or-service> [flags]
```

## Description

`kdiag dns` injects an ephemeral container into the target pod and runs `dig` against the cluster DNS server (CoreDNS). Because the dig runs inside the pod's network namespace, results reflect the DNS resolution the pod itself would see — not what resolves from your workstation or the cluster node.

The target argument can be a pod name or a service name. When a service name is given, kdiag finds a backing Running pod via the service's label selector and builds the FQDN from the service name. When a pod name is given, the FQDN is built from the pod name.

In addition to resolving the target, kdiag checks the health of all CoreDNS pods in `kube-system` and reports which are Ready.

## Flags

No command-specific flags. All [global flags](../README.md#global-flags) apply (`--namespace`, `--image`, `--timeout`, `--output`, etc.).

## Examples

**Resolve a service name from inside one of its backing pods:**

```bash
kdiag dns my-service
```

**Resolve a pod's FQDN from inside the pod:**

```bash
kdiag dns my-pod
```

**Test DNS in a specific namespace:**

```bash
kdiag dns my-service -n production
```

**Get JSON output (useful for scripting):**

```bash
kdiag dns my-service -o json
```

**Verbose mode — see each step as it runs:**

```bash
kdiag dns my-service -v
# [kdiag] building kubernetes client
# [kdiag] resolving target "my-service" in namespace "default"
# [kdiag] resolved "my-service" as service with FQDN "my-service.default.svc.cluster.local"
# [kdiag] checking CoreDNS pod health
# [kdiag] CoreDNS IP: 172.20.0.10
# [kdiag] creating ephemeral container in pod default/my-pod-abc12
# [kdiag] running dig command: [dig my-service.default.svc.cluster.local @172.20.0.10 +noall +answer +stats]
```

## Output

**Table output (default):**

```
DNS Resolution
TARGET        FQDN                                    RESOLVED IPs     QUERY TIME
my-service    my-service.default.svc.cluster.local    172.20.14.7      3ms

CoreDNS Health
POD                      STATUS    READY
coredns-5d78c9869d-6r9j  Running   true
coredns-5d78c9869d-n8qf  Running   true
```

**JSON output (`-o json`):**

```json
{
  "target": "my-service",
  "fqdn": "my-service.default.svc.cluster.local",
  "resolved": ["172.20.14.7"],
  "query_time_ms": 3,
  "coredns_pods": [
    {"name": "coredns-5d78c9869d-6r9j", "status": "Running", "ready": true}
  ]
}
```

A `"warning: DNS query error: ..."` message is printed to stderr if the dig command fails or returns a non-NOERROR status (e.g., NXDOMAIN, SERVFAIL). Partial output is still printed.

## How FQDN Resolution Works

`BuildFQDN` applies the following rules:

- If the name contains a dot, it is used as-is (treated as already qualified).
- Otherwise, the cluster suffix is appended: `<name>.<namespace>.svc.cluster.local`

This means `kdiag dns my-svc` and `kdiag dns my-svc.production.svc.cluster.local` both work as expected.

## RBAC Requirements

`kdiag dns` injects an ephemeral container, which requires:
- `update pods/ephemeralcontainers` in the target namespace
- `create pods/attach` in the target namespace

See [RBAC requirements](../README.md#rbac-requirements).

## Troubleshooting

**"not found as a service or pod in namespace X"**

The argument is not a service or pod in the current namespace. Verify the name and namespace:

```bash
kubectl get svc,pods -n production
kdiag dns my-service -n production
```

**"no Running pods found backing service X"**

The service exists but has no Running pods matching its selector. Check the deployment:

```bash
kubectl get pods -l app=my-service -n production
```

**DNS query returns NXDOMAIN**

The FQDN does not resolve. Common causes: wrong namespace, service not yet registered in DNS, or a CoreDNS misconfiguration. Check CoreDNS pod health in the output and review CoreDNS logs:

```bash
kubectl logs -n kube-system -l k8s-app=kube-dns
```

**DNS query is slow or times out**

Check the CoreDNS Health section of the output. If any pods show `ready: false`, CoreDNS may be degraded. Also check whether CoreDNS pods are under CPU throttling.
