# Architecture Overview

kdiag is a Go CLI built on Cobra. The codebase is divided into command implementations (`cmd/`) and reusable packages (`pkg/`). Commands are thin orchestrators; all Kubernetes interaction is in `pkg/k8s/`.

## Package Layout

```
kdiag/
├── main.go               # Entry point — calls cmd.Execute(); handles ErrHealthCritical sentinel
├── cmd/
│   ├── root.go           # Root command, global flags, flag accessors
│   ├── util.go           # Shared helpers: derefReplicas, eventAge, boolStr
│   ├── shell.go          # shell subcommand
│   ├── capture.go        # capture subcommand
│   ├── dns.go            # dns subcommand
│   ├── connectivity.go   # connectivity subcommand
│   ├── trace.go          # trace subcommand
│   ├── netpol.go         # netpol subcommand
│   ├── logs.go           # logs subcommand
│   ├── inspect.go        # inspect subcommand
│   └── health.go         # health subcommand; defines shared EventSummary type
└── pkg/
    ├── k8s/              # Kubernetes client, ephemeral containers, exec, RBAC, compute, watch
    ├── dns/              # FQDN building, dig output parsing, CoreDNS pod evaluation
    ├── netpol/           # NetworkPolicy matching and rule summarization
    └── output/           # Table and JSON output formatting
```

## cmd/ — Command Layer

### root.go

Initializes `genericclioptions.ConfigFlags` from `k8s.io/cli-runtime`, which registers the full set of kubectl-compatible flags (`--kubeconfig`, `--context`, `--namespace`, `--server`, etc.) without any custom parsing. Additional kdiag-specific flags (`--image`, `--image-pull-secret`, `--timeout`, `--output`, `--verbose`) are registered as persistent flags on the root command.

Exported accessors (`GetDebugImage()`, `GetTimeout()`, `IsVerbose()`, etc.) let subcommands read flag values without accessing package-level variables directly.

`ConfigFlags` is exported so subcommands can pass it to `k8s.NewClient`.

### shell.go and capture.go

Commands follow the same orchestration pattern:

1. Build a `k8s.Client` from `ConfigFlags`
2. Run RBAC pre-flight checks
3. Create an ephemeral container (or node debug pod)
4. Wait for the container to reach Running
5. Attach to stdin/stdout/stderr

Both commands use a two-context strategy: a timeout context (`context.WithTimeout`) for the create and wait phases, and a background context (no timeout) for the attach phase so the user controls session length.

### util.go

Shared package-level helpers used by multiple commands in the `cmd` package:

- **`derefReplicas(p *int32) int32`** — safely dereferences a replica count pointer, returning 1 if nil. Used by `inspect` and `health` when reading `Spec.Replicas`.
- **`eventAge(lastTimestamp metav1.Time, eventTime metav1.MicroTime) string`** — computes a human-readable age string from an event's timestamps. Falls back from `LastTimestamp` to `EventTime` to `"unknown"`.
- **`boolStr(b bool) string`** — converts a bool to `"true"` or `"false"` for table output.

### dns.go, connectivity.go, trace.go, netpol.go

The Phase 2 network diagnostic commands use a different execution model depending on whether they need live data from inside a pod:

**Exec-based commands** (`dns`, `connectivity`): build a `k8s.Client`, resolve target resources, call `k8s.RunInEphemeralContainer` which runs RBAC pre-flight, creates an ephemeral container, waits for Running, then execs a command (`dig` or `curl`/`nc`) and captures stdout/stderr to `bytes.Buffer`. The ephemeral container exits when the command completes.

**Read-only commands** (`trace`, `netpol`): build a `k8s.Client`, fetch pods/services/EndpointSlices/NetworkPolicies via the Kubernetes API, compute results in-process using `pkg/dns` or `pkg/netpol` helpers, then print. No ephemeral containers or exec calls are made.

All four commands support `--output json` via the `pkg/output` printer abstraction.

### logs.go, inspect.go, health.go

The Phase 3 observability commands are read-only (no ephemeral containers) and follow different execution models:

**`logs`** — calls `k8s.ListPodsBySelector` to find matching pods, then spawns one goroutine per pod that calls `k8s.StreamPodLogs`. A `prefixWriter` (defined in `logs.go`) wraps each pod's goroutine output: it prepends a colored `[pod-name]` prefix in text mode or emits JSON-L records in `--output json` mode. A mutex inside `prefixWriter` serializes concurrent writes to `color.Output`. SIGINT/SIGTERM cancels the shared context, which closes all log streams.

**`inspect`** — accepts a `type/name` argument, fetches the resource via the appropriate API group, traverses `ownerReferences` (for pods, up to two hops: Pod → ReplicaSet → Deployment), collects conditions, container statuses, replica counts, and events from `k8s.ListEvents`. Renders four tabular sections or a single `InspectResult` JSON struct.

**`health`** — makes six cluster-wide list calls (nodes, pods, deployments, daemonsets, statefulsets, warning events) with an empty namespace string to span all namespaces. Each resource category is evaluated by a pure function (`evaluateNode`, `evaluatePod`, `evaluateDeployment`, `evaluateDaemonSet`, `evaluateStatefulSet`) that returns a summary and a boolean indicating whether the condition is critical. The `ErrHealthCritical` sentinel is returned when any critical condition is found; `main.go` detects it to suppress error printing while still exiting with code 1.

**Shared type — `EventSummary`**: defined in `health.go` and used by `inspect.go`. Both files are in the same `cmd` package. `summarizeEvents` (in `inspect.go`) populates it from raw `corev1.Event` values using `eventAge` from `util.go`.

## pkg/k8s — Kubernetes Package

### client.go

`NewClient` builds a `*Client` from `genericclioptions.ConfigFlags`. It sets QPS=50 and Burst=100 (higher than kubectl defaults) to avoid rate-limiting during diagnostic workloads. Namespace resolution follows this precedence: `--namespace` flag → kubeconfig context default → `"default"`.

```go
type Client struct {
    Clientset kubernetes.Interface
    Config    *rest.Config
    Namespace string
}
```

### rbac.go

RBAC pre-flight runs before any ephemeral container operation. It uses `SelfSubjectAccessReview` — the same API `kubectl auth can-i` uses — so it checks the caller's actual permissions, including any impersonation or RBAC aggregation.

`CheckEphemeralContainerRBAC` checks two permissions required for ephemeral container workflows:
- `update pods/ephemeralcontainers`
- `create pods/attach`

`CheckSingleRBAC` checks one arbitrary permission (used by node shell to verify `create pods`).

`FormatRBACError` produces a human-readable message listing denied permissions and the RBAC rules a cluster admin must add. kdiag checks permissions before attempting the operation and emits this message if any check fails, rather than letting the Kubernetes API return a cryptic 403.

Background: the standard `admin` ClusterRole does not include `pods/ephemeralcontainers` — this is an upstream Kubernetes gap ([kubernetes#120909](https://github.com/kubernetes/kubernetes/issues/120909)).

### compute.go

`DetectComputeType` determines whether a pod is running on Fargate or a managed EC2 node. Detection order:

1. Check for the `eks.amazonaws.com/fargate-profile` label (set by the EKS Fargate admission webhook at pod creation time)
2. Fall back to checking whether the pod's `spec.nodeName` starts with `fargate-ip-` (the naming convention EKS uses for Fargate virtual nodes)

`IsFargateNode` applies the same node name check independently, used by the node shell path to reject Fargate nodes before any API calls.

This detection informs command behavior:
- Pod shell on Fargate: warning emitted, attempt proceeds
- Node shell on Fargate: immediate error, no Kubernetes calls made

### ephemeral.go

`CreateEphemeralContainer` appends an ephemeral container to a running pod using `UpdateEphemeralContainers`. The container name is `kdiag-<random5>` (using `k8s.io/apimachinery/pkg/util/rand`). When `Command` is nil in `EphemeralContainerOpts`, the image's default entrypoint runs (used for shell). When `Command` is set, it overrides the entrypoint (used for capture, where the command is a tcpdump invocation).

`WaitForContainerRunning` watches the pod via `watchtools.UntilWithSync` until the named ephemeral container reaches Running state. It fails fast on recognized failure reasons (`ErrImagePull`, `ImagePullBackOff`, `CreateContainerError`, `CreateContainerConfigError`) rather than waiting for the timeout.

### exec.go

`AttachToContainer` connects to a running container's stdin/stdout/stderr using the Kubernetes attach subresource. `ExecInContainer` runs a new command in a container using the exec subresource. Both are used by different commands:

- `shell` uses `AttachToContainer` (connects to the container's existing process)
- `capture` uses `AttachToContainer` (tcpdump is already running as the container entrypoint)
- `dns` and `connectivity` use `ExecInContainer` via `RunInEphemeralContainer` for ad-hoc diagnostic commands

`RunInEphemeralContainer` is a higher-level helper (added in Phase 2) that combines RBAC pre-flight, ephemeral container creation, wait-for-running, and exec into a single call. Commands pass an `EphemeralExecOpts` struct specifying pod, namespace, image, command, and stdout/stderr writers.

Both functions build a WebSocket executor with SPDY fallback via `remotecommand.NewFallbackExecutor`. WebSocket is tried first (preferred in Kubernetes 1.29+); on upgrade failure the library falls back to SPDY automatically, ensuring compatibility with older clusters.

When `TTY=true`:
- The local terminal is placed into raw mode via `golang.org/x/term`
- A `terminalSizeQueue` goroutine listens for `SIGWINCH` and forwards terminal dimensions to the remote container, so window resize works during interactive sessions

When `AttachToContainer` is called with `TTY=true`, stderr is not requested from the server — Kubernetes merges stderr into stdout when a TTY is allocated, which is standard terminal behavior.

### watch.go

Kubernetes list and streaming helpers used by the Phase 3 observability commands:

**`ListPodsBySelector(ctx, client, namespace, labelSelector string) ([]corev1.Pod, error)`** — lists pods in the given namespace filtered by a label selector string. Passes the selector directly to the server-side list API; no client-side filtering is performed.

**`ListEvents(ctx, client, namespace, kind, name string) ([]corev1.Event, error)`** — lists events in namespace filtered by `involvedObject.name` and `involvedObject.namespace`. When `kind` is non-empty, `involvedObject.kind` is added to the field selector. Field selector filtering is handled server-side.

**`StreamPodLogs(ctx, client, namespace, podName, containerName string, w io.Writer) error`** — opens a following log stream with `Timestamps: true`. Uses a `bufio.Scanner` with a 1MB buffer to handle long log lines without truncation. Blocks until the stream closes or `ctx` is cancelled.

### nodedbg.go

`CreateNodeDebugPod` builds a pod spec with:
- `nodeName` set to force scheduling on the target node
- `hostPID`, `hostNetwork`, `hostIPC: true` for full host namespace access
- A universal toleration (`operator: Exists`) so the pod schedules on tainted nodes (e.g., nodes being drained or with custom taints)
- The host filesystem mounted as a `hostPath` volume at `/host`
- A privileged security context

The pod is labeled `app.kubernetes.io/managed-by: kdiag` for easy identification.

`DeleteNodeDebugPod` is called via `defer` in the node shell command, ensuring cleanup even when the session ends abnormally. A warning is printed to stderr if deletion fails (so the operator knows to clean up manually).

## pkg/dns — DNS Package

Stateless utilities for DNS diagnostics. No Kubernetes client dependency — all functions take plain Go types.

**`BuildFQDN(name, namespace string) string`** — appends `.namespace.svc.cluster.local` to bare names; returns dotted names unchanged.

**`BuildDigCommand(target, dnsServerIP string) []string`** — returns the `dig` argv slice. When `dnsServerIP` is non-empty, the server is included as `@ip`. Always appends `+noall +answer +stats` to produce parseable output.

**`ParseDigOutput(raw string) (resolved []string, queryTimeMs int64, err error)`** — parses the stdout of the dig invocation above. Extracts A/AAAA answer IPs and query time. Returns an error for empty output or non-NOERROR status (NXDOMAIN, SERVFAIL, etc.). NOERROR with an empty answer section is valid and returns `(nil, queryTimeMs, nil)`.

**`EvaluateCoreDNSPods(pods []corev1.Pod) []CoreDNSPod`** — converts a pod list to `CoreDNSPod` summaries. A pod is `Ready=true` only when every container in `ContainerStatuses` is ready.

## pkg/netpol — NetworkPolicy Package

Stateless utilities for NetworkPolicy analysis. Takes Kubernetes API types as input; produces human-readable summary types as output.

**`MatchingPolicies(policies []networkingv1.NetworkPolicy, podLabels map[string]string) ([]networkingv1.NetworkPolicy, error)`** — filters the policy list to those whose `podSelector` matches the given pod labels. Uses `metav1.LabelSelectorAsSelector` and `labels.Set.Matches`, the same mechanism Kubernetes uses for enforcement. An empty selector (`{}`) matches all pods.

**`SummarizePolicy(policy networkingv1.NetworkPolicy) PolicySummary`** — converts a NetworkPolicy into a `PolicySummary` with human-readable port strings (`TCP/80`, `UDP/53`), peer descriptions (`pods: app=frontend`, `namespaces: env=prod`, `ipBlock: 10.0.0.0/8 except [10.1.0.0/16]`), and fallback strings (`<all sources>`, `<all ports>`, `<all destinations>`) when rule fields are nil or empty.

**`FormatSelector(sel *metav1.LabelSelector) string`** — renders a label selector as `key=value,...`. Returns `<all>` for nil; `<all pods>` for an empty (match-all) selector. Keys are sorted for deterministic output.

## pkg/output — Output Package

The `Printer` interface abstracts table and JSON output behind a common API:

```go
type Printer interface {
    PrintHeader(columns ...string)
    PrintRow(values ...string)
    Flush() error
}
```

`NewPrinter(format, writer)` is the factory used by commands. It returns a `TablePrinter` for `"table"` and a `JSONPrinter` for `"json"`.

`TablePrinter` wraps `text/tabwriter` with 3-space minimum column padding.

`JSONPrinter` implements the `Printer` interface but `PrintHeader` and `PrintRow` are no-ops. For structured JSON output, commands that receive a `*JSONPrinter` directly call `Print(v interface{})`, which marshals and writes indented JSON. Commands that receive the `Printer` interface must type-assert to `*JSONPrinter` to access `Print`.

## Data Flow

### kdiag shell \<pod\>

```
cmd/shell.go
  │
  ├─ k8s.NewClient(ConfigFlags)
  │    └─ REST config from kubeconfig → kubernetes.Interface
  │
  ├─ Clientset.CoreV1().Pods().Get()   ← verify pod exists
  │
  ├─ k8s.DetectComputeType(pod)        ← Fargate warning if applicable
  │
  ├─ k8s.CheckEphemeralContainerRBAC() ← SelfSubjectAccessReview x2
  │
  ├─ k8s.CreateEphemeralContainer()    ← UpdateEphemeralContainers API call
  │    └─ returns containerName
  │
  ├─ k8s.WaitForContainerRunning()     ← watch pod until container Running
  │
  └─ k8s.AttachToContainer()           ← WebSocket/SPDY attach (blocking)
       └─ stdin ↔ container process ↔ stdout/stderr
```

### kdiag capture \<pod\>

```
cmd/capture.go
  │
  ├─ validate --write output directory
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ Clientset.CoreV1().Pods().Get()   ← verify pod exists
  │
  ├─ k8s.CheckEphemeralContainerRBAC()
  │
  ├─ build tcpdump command array       ← [tcpdump, -i, <iface>, flags..., filter]
  │
  ├─ k8s.CreateEphemeralContainer()    ← tcpdump runs as container entrypoint
  │
  ├─ k8s.WaitForContainerRunning()
  │
  ├─ open output file (or os.Stdout)
  │
  └─ k8s.AttachToContainer()           ← streams tcpdump stdout to file/terminal
       └─ SIGINT/SIGTERM cancels context → "Capture interrupted."
       └─ --duration timeout expires  → "Capture complete."
```

### kdiag shell --node \<node\>

```
cmd/shell.go (runNodeShell)
  │
  ├─ k8s.IsFargateNode(nodeName)       ← reject Fargate immediately
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ k8s.CheckSingleRBAC("create","pods","")
  │
  ├─ k8s.CreateNodeDebugPod()          ← privileged pod on target node
  │    └─ defer k8s.DeleteNodeDebugPod()
  │
  ├─ k8s.WaitForPodRunning()
  │
  └─ k8s.AttachToContainer()           ← attach to "debugger" container
```

### kdiag dns \<pod-or-service\>

```
cmd/dns.go
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ Services.Get() OR Pods.Get()  ← resolve target type
  │    └─ dns.BuildFQDN(name, namespace)
  │
  ├─ Pods.List(k8s-app=kube-dns)  ← CoreDNS pod health
  │    └─ dns.EvaluateCoreDNSPods()
  │
  ├─ Services.Get("kube-dns")     ← CoreDNS ClusterIP
  │
  └─ k8s.RunInEphemeralContainer()  ← RBAC + create + wait + exec
       └─ dns.BuildDigCommand(fqdn, coreDNSIP)
       └─ dns.ParseDigOutput(stdout)
       └─ print DNSResult as table or JSON
```

### kdiag connectivity \<source-pod\> \<destination\>

```
cmd/connectivity.go
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ Pods.Get(srcPod)             ← validate source pod is Running
  │
  ├─ resolve destination:
  │    ├─ host:port   → use directly
  │    ├─ Service     → ClusterIP + port (auto-detect protocol from port name/number)
  │    └─ Pod         → PodIP + --port (required)
  │
  ├─ build probe command:
  │    ├─ http → curl -sS --connect-timeout 5 -o /dev/null -w "%{http_code} %{time_total}"
  │    └─ tcp  → nc -zv -w 5 <host> <port>
  │
  └─ k8s.RunInEphemeralContainer()  ← RBAC + create + wait + exec
       └─ parseHTTPResult() or check exec error (TCP)
       └─ print ConnectivityResult as table or JSON
```

### kdiag trace \<source-pod\> \<destination-service\>

```
cmd/trace.go  (read-only, no ephemeral containers)
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ Pods.Get(srcPod)              ← PodIP, NodeName
  │
  ├─ Services.Get(dstService)      ← ClusterIP
  │
  ├─ DiscoveryV1.EndpointSlices.List(LabelServiceName=dstService)
  │    └─ collect ready endpoints (name, IP, NodeName)
  │
  ├─ Nodes.Get() for each unique node  ← AZ labels (best-effort)
  │
  └─ print TraceResult{Source, Service, Endpoints} as table or JSON
```

### kdiag netpol \<pod\>

```
cmd/netpol.go  (read-only, no ephemeral containers)
  │
  ├─ k8s.NewClient(ConfigFlags)
  │
  ├─ Pods.Get(podName)             ← pod labels
  │
  ├─ NetworkingV1.NetworkPolicies.List()
  │
  ├─ netpol.MatchingPolicies(policies, pod.Labels)
  │    └─ LabelSelectorAsSelector + labels.Set.Matches per policy
  │
  ├─ netpol.SummarizePolicy() for each match
  │
  └─ print NetpolResult as structured text or JSON
```

## Dependencies

| Package | Version | Role |
|---------|---------|------|
| `github.com/spf13/cobra` | v1.9.1 | CLI framework |
| `k8s.io/client-go` | v0.32.3 | Kubernetes API client |
| `k8s.io/cli-runtime` | v0.32.3 | kubectl-compatible flag handling |
| `k8s.io/api` | v0.32.3 | Kubernetes API types |
| `k8s.io/apimachinery` | v0.32.3 | Kubernetes API machinery |
| `golang.org/x/term` | v0.25.0 | Raw terminal mode and resize |
| `github.com/fatih/color` | v1.18.0 | ANSI color output for `kdiag logs` pod prefixes |
