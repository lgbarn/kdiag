# kdiag connectivity

Test TCP or HTTP connectivity from a source pod to a destination pod, service, or host:port.

## Synopsis

```
kdiag connectivity <source-pod> <destination> [flags]
```

## Description

`kdiag connectivity` injects an ephemeral container into the source pod and runs a connectivity probe from inside its network namespace. Because the probe runs inside the pod, it tests the actual network path the pod uses — not a path from the node or your workstation.

The destination can be:
- A **service name** — kdiag resolves the ClusterIP and auto-detects port and protocol from the service spec.
- A **pod name** — kdiag uses the pod's IP. `--port` is required.
- A **host:port string** — used directly; no Kubernetes lookup performed.

Protocol defaults to `tcp` unless the destination is a service with an HTTP-named port or port 80/443, in which case `http` is used automatically.

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--port` | `-p` | `0` | Destination port. Required when destination is a pod. |
| `--protocol` | — | `tcp` | Protocol to test: `tcp` or `http` |

All [global flags](../README.md#global-flags) apply (`--namespace`, `--image`, `--timeout`, `--output`, etc.).

## Examples

**Test TCP connectivity to a service (port auto-detected from service spec):**

```bash
kdiag connectivity my-pod my-service
```

**Test HTTP connectivity:**

```bash
kdiag connectivity my-pod my-service --protocol http
```

**Test connectivity to another pod on a specific port:**

```bash
kdiag connectivity my-pod other-pod --port 8080
```

**Test a raw host:port (bypasses Kubernetes lookups):**

```bash
kdiag connectivity my-pod 10.0.1.42:5432
```

**Test connectivity to an external endpoint:**

```bash
kdiag connectivity my-pod 8.8.8.8:53
```

**Get JSON output:**

```bash
kdiag connectivity my-pod my-service -o json
```

**Test in a specific namespace:**

```bash
kdiag connectivity my-pod my-service -n production
```

## Output

**Table output (default):**

```
SOURCE     DESTINATION      PROTOCOL   STATUS   LATENCY   DETAILS
my-pod     172.20.14.7:80   http       OK       42ms      HTTP 200
```

A failed connection:

```
SOURCE     DESTINATION      PROTOCOL   STATUS    LATENCY   DETAILS
my-pod     10.0.1.5:5432    tcp        FAILED    5001ms    connection refused
```

**JSON output (`-o json`):**

```json
{
  "source": "my-pod",
  "destination": "172.20.14.7:80",
  "protocol": "http",
  "success": true,
  "latency_ms": 42,
  "status_code": 200
}
```

Fields present only when relevant: `status_code` (HTTP only), `error` (on failure).

## Protocol Behavior

**TCP** — uses `nc -zv -w 5 <host> <port>`. Success is determined by exit code.

**HTTP** — uses `curl -sS --connect-timeout 5 -o /dev/null -w "%{http_code} %{time_total}"`. HTTP 2xx and 3xx responses are treated as success. Latency is taken from curl's `time_total` (seconds, converted to milliseconds).

**Auto-detection for services**: if the service port name contains "http" or the port number is 80 or 443, the protocol is set to `http` regardless of `--protocol`.

## RBAC Requirements

`kdiag connectivity` injects an ephemeral container, which requires:
- `update pods/ephemeralcontainers` in the target namespace
- `create pods/attach` in the target namespace

See [RBAC requirements](../README.md#rbac-requirements).

## Troubleshooting

**"--port is required when destination is a pod"**

Pod destinations have no port metadata. Specify the port explicitly:

```bash
kdiag connectivity my-pod other-pod --port 8080
```

**STATUS: FAILED, LATENCY: ~5000ms**

The connection timed out. The destination is unreachable. Check NetworkPolicies with `kdiag netpol`, security group rules, and whether the destination pod/service is healthy.

**STATUS: FAILED, DETAILS: connection refused**

The destination is reachable (routing works) but nothing is listening on that port. Verify the service port configuration and that the destination application is running.

**HTTP STATUS: FAILED, DETAILS: HTTP 401 or 403**

The endpoint is reachable and responded. Authentication or authorization is blocking the request, not the network. This means connectivity itself is working.
