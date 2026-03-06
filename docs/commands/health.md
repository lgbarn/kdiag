# kdiag health

Cluster-wide health report: nodes, pods, controllers, and recent warning events.

## Synopsis

```
kdiag health [flags]
```

## Description

`kdiag health` scans the entire cluster in a single command and reports issues across nodes, pods, deployments, daemonsets, and statefulsets. It is designed to be used as a health gate — it exits with code 1 when critical issues are found, making it suitable for CI pipelines and alerting scripts.

No ephemeral containers are injected. All data is read from the Kubernetes API (6 list calls across all namespaces).

## Flags

No command-specific flags. All [global flags](../README.md#global-flags) apply (`--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Run a cluster health check:**

```bash
kdiag health
```

**Use as a CI gate (exits 1 on critical issues):**

```bash
kdiag health || pagerduty-alert "cluster health check failed"
```

**Get structured JSON output:**

```bash
kdiag health -o json
```

**Check health in verbose mode:**

```bash
kdiag health -v
```

## Output

**Table output (default):**

```
Node Health
NAME                                          STATUS   READY   MEM_PRESSURE   DISK_PRESSURE   PID_PRESSURE
ip-10-0-1-42.us-east-1.compute.internal      Ready    true    false          false           false
ip-10-0-1-55.us-east-1.compute.internal      Ready    true    false          false           false

Pods with Issues
NAMESPACE     NAME                      PHASE     REASON
production    myapp-6d9f4b-xkp2j        Running   CrashLoopBackOff

Controller Health Issues
NAMESPACE     KIND         NAME     READY   DESIRED   STATUS
production    Deployment   myapp    1       3         Degraded

Recent Warning Events (showing up to 20)
NAMESPACE     OBJECT              REASON              MESSAGE                        COUNT   AGE
production    Pod/myapp-abc12     BackOff             Back-off restarting failed...  14      3m10s

Summary: 2 nodes, 1 pod issues, 1 controller issues, 1 warning events
Status: CRITICAL
```

Sections with no issues are omitted from the output. A clean cluster shows only the Node Health section and a `Status: OK` summary.

**JSON output (`-o json`):**

```json
{
  "nodes": [
    {
      "name": "ip-10-0-1-42.us-east-1.compute.internal",
      "status": "Ready",
      "ready": true,
      "memory_pressure": false,
      "disk_pressure": false,
      "pid_pressure": false
    }
  ],
  "pod_issues": [
    {
      "namespace": "production",
      "name": "myapp-6d9f4b-xkp2j",
      "phase": "Running",
      "reason": "CrashLoopBackOff"
    }
  ],
  "controllers": [
    {
      "namespace": "production",
      "kind": "Deployment",
      "name": "myapp",
      "ready": 1,
      "desired": 3,
      "status": "Degraded"
    }
  ],
  "events": [...],
  "critical": true
}
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | All checks passed — no critical conditions found |
| `1` | One or more critical conditions found |

Critical conditions:
- Node: `NotReady`, `MemoryPressure`, `DiskPressure`
- Pod: `Failed`, `CrashLoopBackOff`
- Controller: Deployment `Unavailable` or `DeadlineExceeded`; DaemonSet or StatefulSet `Unavailable`

Non-critical (reported but do not set exit code 1):
- Node: `PIDPressure`
- Pod: `Pending`, `Unknown`
- Controller: `Degraded` (ready < desired, but not zero)

## What Is Checked

**Nodes** — evaluates `Ready`, `MemoryPressure`, `DiskPressure`, and `PIDPressure` conditions across all nodes.

**Pods** — scans all pods cluster-wide for `Failed`, `Pending`, `Unknown` phases and `CrashLoopBackOff` container state.

**Controllers** — checks Deployments, DaemonSets, and StatefulSets cluster-wide. Only unhealthy controllers appear in the report.

**Events** — collects up to 100 `Warning`-type events cluster-wide and displays the 20 most recent.

## Troubleshooting

**"failed to list nodes"**

kdiag requires cluster-wide read access. Ensure your kubeconfig context has at minimum:
- `get`, `list` on `nodes`
- `get`, `list` on `pods` (all namespaces)
- `get`, `list` on `deployments`, `daemonsets`, `statefulsets` (all namespaces)
- `get`, `list` on `events` (all namespaces)

**Status: CRITICAL but no obvious issues in the table**

Check the Events section for context. Also check whether the issue is in a namespace not normally visible with your default context (e.g., `kube-system`). Run `kdiag health -o json | jq .pod_issues` to see the full list without truncation.

**False positive: Pending pod during a rolling deployment**

Pending pods during a controlled rollout are expected. Consider running `kdiag health` after a deployment stabilizes, or filter the JSON output in your pipeline to exclude known transient states.
