# kdiag shell

Launch an interactive debug shell inside a pod or directly on an EC2 node.

## Synopsis

```
kdiag shell <pod-name> [flags]
kdiag shell --node <node-name> [flags]
```

## Description

`kdiag shell` provides two debug modes:

**Pod shell** — injects an ephemeral container into a running pod using the Kubernetes ephemeral containers API. The debug container shares the pod's network namespace, making it ideal for diagnosing network issues from inside a pod's exact network context. The ephemeral container is garbage-collected by the kubelet after the session ends.

**Node shell** — creates a privileged pod directly on the target EC2 node with host PID, network, and IPC namespaces and the host filesystem mounted at `/host`. The pod is deleted when the session ends. Not supported on Fargate nodes.

## Flags

| Flag | Description |
|------|-------------|
| `--node <name>` | Target node name for node-level debugging. Mutually exclusive with `<pod-name>`. |

All [global flags](../README.md#global-flags) apply (`--namespace`, `--image`, `--timeout`, etc.).

## Examples

**Shell into a pod in the current namespace:**

```bash
kdiag shell my-pod
```

**Shell into a pod in a specific namespace:**

```bash
kdiag shell my-pod -n production
```

**Shell into a pod using a specific kubeconfig context:**

```bash
kdiag shell my-pod --context staging-cluster
```

**Shell on a node (requires the full node name as shown by `kubectl get nodes`):**

```bash
kdiag shell --node ip-10-0-1-42.us-east-1.compute.internal
```

**Use a custom debug image from a private registry:**

```bash
kdiag shell my-pod --image my-registry.internal/netshoot:latest --image-pull-secret regcred
```

**Verbose mode — see each step as it runs:**

```bash
kdiag shell my-pod -v
# [kdiag] building kubernetes client
# [kdiag] fetching pod default/my-pod
# [kdiag] detected compute type: managed
# [kdiag] checking RBAC permissions
# [kdiag] creating ephemeral container in pod default/my-pod
# [kdiag] waiting for container "kdiag-ab3fx" to start
# [kdiag] attaching to container "kdiag-ab3fx"
```

## Session Lifecycle

### Pod shell

1. kdiag verifies the pod exists and detects whether it runs on Fargate.
2. RBAC is checked — requires `update pods/ephemeralcontainers` and `create pods/attach`.
3. An ephemeral container named `kdiag-<random5>` is injected using the debug image.
4. kdiag waits (up to `--timeout`, default 30s) for the container to reach Running state.
5. stdin/stdout/stderr are attached. Terminal resize events (SIGWINCH) are forwarded automatically.
6. On exit, the ephemeral container is left for the kubelet to garbage-collect.

### Node shell

1. kdiag rejects Fargate node names immediately (identified by the `fargate-ip-` prefix).
2. RBAC is checked — requires `create pods` in the target namespace.
3. A privileged pod named `kdiag-node-<nodename>-<random5>` is created on the target node with:
   - `hostPID`, `hostNetwork`, `hostIPC` enabled
   - Host filesystem mounted at `/host`
   - A universal toleration so the pod schedules on tainted nodes
4. kdiag waits (up to `--timeout`) for the pod to reach Running.
5. stdin/stdout/stderr are attached.
6. On session exit, the debug pod is deleted automatically.

## RBAC Requirements

**Pod shell** requires both:
- `update pods/ephemeralcontainers` in the target namespace
- `create pods/attach` in the target namespace

**Node shell** requires:
- `create pods` in the target namespace

If any permission is missing, kdiag prints the denied permissions and the RBAC rules a cluster admin needs to add. See [RBAC requirements](../README.md#rbac-requirements).

## Fargate Behavior

- **Pod on Fargate**: kdiag prints a warning to stderr before attempting the ephemeral container injection. The operation may or may not succeed depending on your Fargate platform version.
- **Node is Fargate**: `kdiag shell --node <fargate-node>` exits immediately with an error. Fargate virtual nodes do not support privileged pods.

## Troubleshooting

**"ephemeral container failed to start: reason=ErrImagePull"**

The debug image cannot be pulled. Either the image name is wrong, the node has no internet access, or you need `--image-pull-secret`.

**"error waiting for ephemeral container to start" (timeout)**

Increase `--timeout` (e.g., `--timeout 2m`) or check whether the node is under resource pressure.

**"node X appears to be a Fargate virtual node"**

You passed a Fargate node name to `--node`. Node-level debugging is not supported on Fargate. Debug the pods running on that Fargate node using `kdiag shell <pod-name>` instead.
