# kdiag trace

Map the Kubernetes network path from a source pod through a service to its endpoint pods.

## Synopsis

```
kdiag trace <source-pod> <destination-service> [flags]
```

## Description

`kdiag trace` shows the full logical network path from a source pod to a service's backing endpoints without injecting any containers or generating traffic. It is a pure read operation — no ephemeral containers are created.

For each hop, kdiag reports the pod name, namespace, IP address, node name, and availability zone. This makes it easy to identify cross-AZ traffic, misconfigured service selectors, or endpoints on tainted or unschedulable nodes.

AZ lookup is best-effort: kdiag checks the `topology.kubernetes.io/zone` label first, then falls back to `failure-domain.beta.kubernetes.io/zone`. If node metadata is unavailable, the AZ column is left blank without failing the command.

## Flags

No command-specific flags. All [global flags](../README.md#global-flags) apply (`--namespace`, `--image`, `--timeout`, `--output`, etc.).

Note: `--timeout` applies to Kubernetes API calls (pod/service/endpoint resolution), not to any traffic probe.

## Examples

**Trace the path from a pod to a service:**

```bash
kdiag trace my-pod my-service
```

**Trace in a specific namespace:**

```bash
kdiag trace my-pod my-service -n production
```

**Get JSON output for scripting or further analysis:**

```bash
kdiag trace my-pod my-service -o json
```

**Verbose mode — see API calls as they happen:**

```bash
kdiag trace my-pod my-service -v
# [kdiag] resolving source pod "my-pod" in namespace "default"
# [kdiag] source pod IP: 10.0.1.5, node: ip-10-0-1-5.us-east-1.compute.internal
# [kdiag] resolving destination service "my-service"
# [kdiag] service ClusterIP: 172.20.14.7
# [kdiag] listing EndpointSlices for service "my-service"
# [kdiag] found 3 ready endpoints; resolving node AZs
```

## Output

**Table output (default):**

```
Network Path: my-pod -> my-service

HOP        NAME              NAMESPACE   IP            NODE                                      AZ
Source     my-pod            default     10.0.1.5      ip-10-0-1-5.us-east-1.compute.internal    us-east-1a
Service    my-service        default     172.20.14.7
Endpoint   my-service-xk2m   default     10.0.2.8      ip-10-0-2-8.us-east-1.compute.internal    us-east-1b
Endpoint   my-service-p9qr   default     10.0.3.11     ip-10-0-3-11.us-east-1.compute.internal   us-east-1c
```

**JSON output (`-o json`):**

```json
{
  "source": {
    "name": "my-pod",
    "namespace": "default",
    "ip": "10.0.1.5",
    "node_name": "ip-10-0-1-5.us-east-1.compute.internal",
    "node_az": "us-east-1a"
  },
  "service": {
    "name": "my-service",
    "namespace": "default",
    "ip": "172.20.14.7"
  },
  "endpoints": [
    {
      "name": "my-service-xk2m",
      "namespace": "default",
      "ip": "10.0.2.8",
      "node_name": "ip-10-0-2-8.us-east-1.compute.internal",
      "node_az": "us-east-1b"
    }
  ]
}
```

## How Endpoints Are Resolved

kdiag uses the **EndpointSlices API** (`discovery.k8s.io/v1`) rather than the older Endpoints API. Only endpoints where `Conditions.Ready` is `true` (or `nil`) are included — not-ready endpoints are silently skipped. If a `TargetRef` pointing to a pod is present on an endpoint, the pod name is shown; otherwise the endpoint IP is used as the name.

## RBAC Requirements

`kdiag trace` is read-only and requires:
- `get pods` in the target namespace
- `get services` in the target namespace
- `list endpointslices` in the target namespace (`discovery.k8s.io` API group)
- `get nodes` cluster-wide (for AZ resolution; failures here are non-fatal)

These permissions are typically included in the standard `view` ClusterRole.

## Troubleshooting

**No endpoints shown**

The service exists but has no ready endpoints. Check that the service selector matches pod labels and that pods are Running:

```bash
kubectl get pods -l app=my-service -n production
kubectl describe svc my-service -n production
```

**AZ column is blank**

kdiag could not retrieve node metadata. This is non-fatal. Common causes: no `get nodes` permission, or the endpoint is on a Fargate virtual node which does not expose zone labels in the same way.

**"service X not found in namespace Y"**

The second argument must be a service name, not a pod name. `kdiag trace` always resolves the destination as a service. To test pod-to-pod connectivity, use `kdiag connectivity`.
