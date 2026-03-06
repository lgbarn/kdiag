# kdiag inspect

Show enriched resource details: ownership chain, conditions, container status, and related events.

## Synopsis

```
kdiag inspect <type/name> [flags]
```

## Description

`kdiag inspect` fetches a Kubernetes resource and displays a structured summary that combines information you would otherwise pull from several `kubectl describe` and `kubectl get` invocations: the controller ownership chain, status conditions, container states with restart counts, replica health, and recent events.

No ephemeral containers are injected. All data is read from the Kubernetes API.

**Supported resource types:** `pod`, `deployment`, `replicaset`, `daemonset`, `statefulset`

## Flags

No command-specific flags. All [global flags](../README.md#global-flags) apply (`--namespace`, `--output`, `--timeout`, `--verbose`, etc.).

## Examples

**Inspect a pod:**

```bash
kdiag inspect pod/myapp-6d9f4b-xkp2j
```

**Inspect a deployment:**

```bash
kdiag inspect deployment/myapp
```

**Inspect a DaemonSet in a specific namespace:**

```bash
kdiag inspect daemonset/aws-node -n kube-system
```

**Get JSON output (useful for scripting or CI):**

```bash
kdiag inspect deployment/myapp -o json
```

**Verbose mode — log each API call:**

```bash
kdiag inspect pod/myapp-6d9f4b-xkp2j -v
```

## Output

**Table output (default):**

```
Resource: Deployment/myapp
Namespace: production

Owner Chain: Deployment/myapp

Conditions:
  TYPE           STATUS   REASON              MESSAGE
  Available      True     MinimumAvailable
  Progressing    True     NewReplicaSetAvailable

Replicas:
  DESIRED   READY   AVAILABLE   UPDATED
  3         3       3           3

Events:
  TYPE     REASON              MESSAGE                       COUNT   AGE
  Normal   ScalingReplicaSet   Scaled up replica set...      1       5m2s
```

For pods, a Containers section is shown in place of Replicas:

```
Containers:
  NAME    READY   RESTARTS   STATE     DETAIL
  app     true    0          Running
  proxy   false   3          Waiting   CrashLoopBackOff
```

**JSON output (`-o json`):**

```json
{
  "resource_kind": "Deployment",
  "resource_name": "myapp",
  "namespace": "production",
  "owner_chain": [
    {"kind": "Deployment", "name": "myapp"}
  ],
  "conditions": [
    {"type": "Available", "status": "True", "reason": "MinimumAvailable", "message": ""}
  ],
  "replicas": {
    "desired": 3,
    "ready": 3,
    "available": 3,
    "updated": 3
  },
  "events": [
    {"namespace": "production", "type": "Normal", "reason": "ScalingReplicaSet", "message": "...", "count": 1, "age": "5m2s"}
  ]
}
```

`containers` is present for pods; `replicas` is present for deployments, replicasets, daemonsets, and statefulsets. Both are omitted when not applicable.

## Owner Chain

For pods, `inspect` traverses `ownerReferences` to build the full chain:

- **Pod owned by ReplicaSet**: kdiag fetches the ReplicaSet to check whether it is owned by a Deployment. If so, the chain is `Pod/name -> ReplicaSet/name -> Deployment/name`.
- **Pod owned by DaemonSet or StatefulSet**: chain stops at the controller.
- **RS lookup failure**: if the ReplicaSet cannot be fetched, the chain is truncated at the ReplicaSet with a verbose-mode warning. The rest of the output is still printed.

For workload resources (Deployment, DaemonSet, StatefulSet), the chain shows only the resource itself — these are root controllers.

## Troubleshooting

**"invalid resource argument: expected type/name format"**

The argument must use a slash separator:

```bash
kdiag inspect pod/my-pod        # correct
kdiag inspect pod my-pod        # wrong
```

**"unsupported resource type"**

Only `pod`, `deployment`, `replicaset`, `daemonset`, and `statefulset` are supported.

**"pod X not found in namespace Y"**

Verify the name and namespace:

```bash
kubectl get pods -n production
kdiag inspect pod/my-pod -n production
```

**Events section is empty**

No events reference this resource. Events are scoped to the resource's namespace and filtered by `involvedObject.name` and `involvedObject.kind`. Events expire after ~1 hour by default.
