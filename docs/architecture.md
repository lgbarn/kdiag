# Architecture Overview

kdiag is a Go CLI built on Cobra. The codebase is divided into command implementations (`cmd/`) and reusable packages (`pkg/`). Commands are thin orchestrators; all Kubernetes interaction is in `pkg/k8s/`.

## Package Layout

```
kdiag/
├── main.go               # Entry point — calls cmd.Execute()
├── cmd/
│   ├── root.go           # Root command, global flags, flag accessors
│   ├── shell.go          # shell subcommand
│   └── capture.go        # capture subcommand
└── pkg/
    ├── k8s/              # Kubernetes client, ephemeral containers, exec, RBAC, compute
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
- Future commands (`dns`, `connectivity`) will use `ExecInContainer` for ad-hoc commands

Both functions build a WebSocket executor with SPDY fallback via `remotecommand.NewFallbackExecutor`. WebSocket is tried first (preferred in Kubernetes 1.29+); on upgrade failure the library falls back to SPDY automatically, ensuring compatibility with older clusters.

When `TTY=true`:
- The local terminal is placed into raw mode via `golang.org/x/term`
- A `terminalSizeQueue` goroutine listens for `SIGWINCH` and forwards terminal dimensions to the remote container, so window resize works during interactive sessions

When `AttachToContainer` is called with `TTY=true`, stderr is not requested from the server — Kubernetes merges stderr into stdout when a TTY is allocated, which is standard terminal behavior.

### nodedbg.go

`CreateNodeDebugPod` builds a pod spec with:
- `nodeName` set to force scheduling on the target node
- `hostPID`, `hostNetwork`, `hostIPC: true` for full host namespace access
- A universal toleration (`operator: Exists`) so the pod schedules on tainted nodes (e.g., nodes being drained or with custom taints)
- The host filesystem mounted as a `hostPath` volume at `/host`
- A privileged security context

The pod is labeled `app.kubernetes.io/managed-by: kdiag` for easy identification.

`DeleteNodeDebugPod` is called via `defer` in the node shell command, ensuring cleanup even when the session ends abnormally. A warning is printed to stderr if deletion fails (so the operator knows to clean up manually).

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

## Dependencies

| Package | Version | Role |
|---------|---------|------|
| `github.com/spf13/cobra` | v1.9.1 | CLI framework |
| `k8s.io/client-go` | v0.32.3 | Kubernetes API client |
| `k8s.io/cli-runtime` | v0.32.3 | kubectl-compatible flag handling |
| `k8s.io/api` | v0.32.3 | Kubernetes API types |
| `k8s.io/apimachinery` | v0.32.3 | Kubernetes API machinery |
| `golang.org/x/term` | v0.25.0 | Raw terminal mode and resize |
