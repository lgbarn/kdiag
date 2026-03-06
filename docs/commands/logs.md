# kdiag logs

Tail logs from pods matching a label selector with color-coded, per-pod prefixes.

## Synopsis

```
kdiag logs -l <selector> [flags]
```

## Description

`kdiag logs` opens a concurrent log stream for every pod that matches the given label selector. Each pod's lines are prefixed with a colored `[pod-name]` tag so you can distinguish output when multiple pods are streaming simultaneously. Ctrl-C (SIGINT) closes all streams cleanly.

No ephemeral containers are injected. Logs are streamed directly using the Kubernetes log API with `follow: true` and `timestamps: true`.

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--selector` | `-l` | — | Label selector for pod matching. **Required.** Example: `app=myapp` |
| `--filter` | — | — | Only print log lines containing this string |
| `--max-pods` | — | `10` | Maximum number of concurrent pod log streams. Excess pods are skipped with a warning. |
| `--container` | `-c` | — | Container name to stream. Omit to use the pod's default container. |

All [global flags](../README.md#global-flags) apply (`--namespace`, `--timeout`, `--output`, etc.).

## Examples

**Tail all pods with `app=myapp`:**

```bash
kdiag logs -l app=myapp
```

**Filter to lines containing "error":**

```bash
kdiag logs -l app=myapp --filter error
```

**Stream a specific container across multiple pods:**

```bash
kdiag logs -l app=myapp -c sidecar
```

**Limit to 5 concurrent streams:**

```bash
kdiag logs -l app=myapp --max-pods 5
```

**Emit JSON-L for log aggregation:**

```bash
kdiag logs -l app=myapp -o json
```

**Tail in a specific namespace:**

```bash
kdiag logs -l app=myapp -n production
```

## Output

**Table output (default):**

```
[myapp-6d9f4b-xkp2j] 2026-03-05T10:01:23Z started listening on :8080
[myapp-6d9f4b-r9qn8] 2026-03-05T10:01:24Z started listening on :8080
[myapp-6d9f4b-xkp2j] 2026-03-05T10:01:31Z GET /health 200
```

Each pod is assigned a distinct color from a ten-color palette (cyan, green, yellow, magenta, blue, and their hi-intensity variants), cycling if there are more than ten pods.

**JSON output (`-o json`):**

Each log line is emitted as a JSON-L record on stdout:

```json
{"pod":"myapp-6d9f4b-xkp2j","timestamp":"2026-03-05T10:01:23Z","message":"started listening on :8080"}
{"pod":"myapp-6d9f4b-r9qn8","timestamp":"2026-03-05T10:01:24Z","message":"started listening on :8080"}
```

Timestamp is extracted from the Kubernetes log stream prefix (RFC3339). If the log line has no timestamp prefix, `timestamp` is an empty string.

## Behavior Notes

- If more pods match `--selector` than `--max-pods` allows, a warning is printed to stderr and only the first N pods are streamed. Order follows the Kubernetes API response order.
- If a pod's log stream ends unexpectedly (pod evicted, node failure, etc.), a warning is printed to stderr and the remaining streams continue.
- `--filter` is applied per-line after timestamp stripping. It is a plain substring match, not a regex.
- Color is disabled automatically when stdout is not a TTY (e.g., piped to a file or `grep`).

## Troubleshooting

**"--selector / -l flag is required"**

The `-l` flag is mandatory:

```bash
kdiag logs -l app=myapp
```

**"no pods found matching selector"**

No Running pods match the selector in the target namespace. Verify the selector and namespace:

```bash
kubectl get pods -l app=myapp -n production
kdiag logs -l app=myapp -n production
```

**"--max-pods must be at least 1"**

`--max-pods 0` is not valid. Use a positive integer.

**Log stream ends immediately**

The pod may have terminated or its container exited. Check pod status:

```bash
kubectl get pods -l app=myapp
```
