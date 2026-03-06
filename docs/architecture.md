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
│   ├── health.go         # health subcommand; defines shared EventSummary type
│   └── eks/
│       ├── eks.go        # EksCmd parent, Init() pattern, shared flag accessors
│       ├── cni.go        # eks cni subcommand
│       ├── sg.go         # eks sg subcommand
│       └── node.go       # eks node subcommand
└── pkg/
    ├── k8s/              # Kubernetes client, ephemeral containers, exec, RBAC, compute, watch
    ├── aws/              # EC2 API client, ENI queries, security group lookups, EKS detection
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

### cmd/eks/

The `eks` command group is a separate Go package (`package eks`) in `cmd/eks/`. This isolation means it can declare its own package-level variables for the shared flag pointers without polluting the top-level `cmd` package.

#### eks.go — Init() pattern

Because `cmd/eks/` is a separate package, it cannot access the root command's flag variables directly. Instead, `root.go` calls `eks.Init(root, configFlags, outputFormat, timeout, verbose)` during `init()`, which stores the flag pointers in package-level variables and registers `EksCmd` on the root command.

```
root.go: cmd.Init() call
  │
  └─ eks.Init(root, configFlags, outputFormat, timeout, verbose)
       ├─ stores shared flag pointers
       ├─ registers --aws-profile and --aws-region as persistent flags on EksCmd
       └─ root.AddCommand(EksCmd)
```

This pattern lets each `eks` subcommand call `getOutputFormat()`, `getTimeout()`, and `isVerbose()` as package-local accessors backed by the same underlying flag values as the rest of the CLI.

Each subcommand (`cni.go`, `sg.go`, `node.go`) registers itself on `EksCmd` via its own `init()` function.

#### EKS cluster guard

Every `eks` subcommand calls `requireEKS(host)` before making any AWS API calls. This function checks that the kubeconfig server endpoint matches the EKS hostname pattern (`<id>.<az>.<region>.eks.amazonaws.com`) by delegating to `pkg/aws.IsEKSCluster`. Commands that inadvertently target a non-EKS cluster (e.g., a local kind cluster) fail immediately with a descriptive error rather than making EC2 API calls that would return nothing useful.

#### EC2 client construction

The shared `newEC2Client(ctx, host)` helper resolves the AWS region (explicit `--aws-region` flag or parsed from the EKS host via `pkg/aws.RegionFromHost`), then calls `pkg/aws.NewEC2Client`. All three subcommands use this helper.

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

`DetectNodeComputeType` operates on a `*corev1.Node` rather than a pod and adds a third compute type. Detection order:

1. Check for the `eks.amazonaws.com/compute-type=auto` label → `ComputeTypeAutoMode`
2. Check for the `fargate-ip-` node name prefix → `ComputeTypeFargate`
3. Default → `ComputeTypeManaged`

This three-way classification is used by `kdiag eks node`, which includes Auto Mode nodes in its report (with a note) while still skipping Fargate nodes.

The `ComputeType` constants are:

| Constant | Value | Meaning |
|----------|-------|---------|
| `ComputeTypeManaged` | `"managed"` | Standard EC2 managed node group |
| `ComputeTypeFargate` | `"fargate"` | AWS Fargate virtual node |
| `ComputeTypeAutoMode` | `"auto-mode"` | EKS Auto Mode node |

This detection informs command behavior:
- Pod shell on Fargate: warning emitted, attempt proceeds
- Node shell on Fargate: immediate error, no Kubernetes calls made
- `eks sg` on Fargate pod: immediate error
- `eks cni` and `eks node`: Fargate nodes skipped; Auto Mode nodes included with a note

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

## pkg/aws — AWS Package

Low-level EC2 API wrappers and EKS cluster detection utilities. No Kubernetes client dependency — all functions take plain Go types and the `EC2API` interface.

### ec2iface.go

`EC2API` is a minimal interface over the EC2 client surface:

```go
type EC2API interface {
    DescribeInstances(...)       (*ec2.DescribeInstancesOutput, error)
    DescribeInstanceTypes(...)   (*ec2.DescribeInstanceTypesOutput, error)
    DescribeNetworkInterfaces(...) (*ec2.DescribeNetworkInterfacesOutput, error)
    DescribeSecurityGroups(...)  (*ec2.DescribeSecurityGroupsOutput, error)
}
```

Using an interface (rather than the concrete `*ec2.Client`) allows tests to inject a mock without hitting AWS. A compile-time assertion `var _ EC2API = (*ec2.Client)(nil)` enforces that the real client always satisfies the interface.

### client.go

`NewEC2Client(ctx, region, profile string) (EC2API, error)` loads AWS config via the standard SDK credential chain (environment variables, shared config files, IAM role, IRSA). Empty `region` and `profile` strings are silently ignored.

A credential pre-flight check (`cfg.Credentials.Retrieve`) runs before returning the client, so callers receive a clear error message with remediation steps before any real API call is attempted.

### detect.go

`IsEKSCluster(host string) bool` and `RegionFromHost(host string) (string, error)` parse the EKS API server endpoint to validate it and extract the AWS region. The expected hostname format is `<id>.<az-code>.<region>.eks.amazonaws.com`.

`ParseInstanceID(providerID string) (string, error)` extracts the EC2 instance ID from a node's `spec.providerID` in `aws:///<az>/<instance-id>` format.

### eni.go

**`ListNodeENIs(ctx, api, instanceID) (*NodeENIInfo, error)`** — returns all ENIs attached to the instance with their private IP counts and security group IDs. Wraps `DescribeNetworkInterfaces` filtered by `attachment.instance-id`.

**`GetInstanceTypeLimits(ctx, api, instanceTypes []string) (map[string]*InstanceLimits, error)`** — batch-queries ENI and IP-per-ENI limits for a list of instance types via `DescribeInstanceTypes`. The caller collects unique instance types from all nodes and makes a single API call.

### sg.go

**`GetSecurityGroupDetails(ctx, api, groupIDs []string) ([]SecurityGroupDetail, error)`** — fetches full inbound and outbound rules for the given security group IDs. Maps EC2 `IpPermission` types to the kdiag `SGRule` domain type, converting protocol `-1` to `"all"`.

**`GetENISecurityGroups(ctx, api, eniID string) ([]string, error)`** — returns security group IDs for a branch ENI (used by `eks sg` for Security Groups for Pods).

**`GetNodePrimaryENISecurityGroups(ctx, api, instanceID string) ([]string, error)`** — returns security group IDs for the primary ENI (device index 0) of an EC2 instance.

**`ParsePodENIAnnotation(annotation string) ([]PodENIAnnotation, error)`** — unmarshals the JSON value of the `vpc.amazonaws.com/pod-eni` annotation used by the VPC CNI Security Groups for Pods feature.

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

### kdiag eks cni

```
cmd/eks/cni.go
  │
  ├─ k8s.NewClient(configFlags)
  │
  ├─ requireEKS(host)              ← reject non-EKS clusters immediately
  │
  ├─ AppsV1.DaemonSets("kube-system").Get("aws-node")
  │    └─ extract DaemonSetStatus + CNIConfig from env vars
  │
  ├─ newEC2Client(ctx, host)       ← resolveRegion(host) → pkg/aws.NewEC2Client
  │
  ├─ CoreV1.Nodes().List()
  │    └─ classify: skip Fargate, extract instanceType + instanceID per node
  │
  ├─ aws.GetInstanceTypeLimits(uniqueTypes)   ← DescribeInstanceTypes (batched)
  │
  └─ per node: aws.ListNodeENIs(instanceID)   ← DescribeNetworkInterfaces
       └─ calculate utilization; flag EXHAUSTED at >= 85%
       └─ print CNIReport as table (3 sections) or JSON
```

### kdiag eks sg \<pod\>

```
cmd/eks/sg.go
  │
  ├─ k8s.NewClient(configFlags)
  │
  ├─ requireEKS(host)
  │
  ├─ CoreV1.Pods(namespace).Get(podName)
  │    └─ k8s.DetectComputeType(pod) → reject Fargate pods
  │
  ├─ newEC2Client(ctx, host)
  │
  ├─ pod has vpc.amazonaws.com/pod-eni annotation?
  │    ├─ YES → aws.ParsePodENIAnnotation() → eniID
  │    │         aws.GetENISecurityGroups(eniID)   ← DescribeNetworkInterfaces
  │    └─ NO  → CoreV1.Nodes().Get(pod.Spec.NodeName)
  │              aws.ParseInstanceID(node.Spec.ProviderID)
  │              aws.GetNodePrimaryENISecurityGroups(instanceID) ← DescribeNetworkInterfaces
  │
  └─ aws.GetSecurityGroupDetails(sgIDs)   ← DescribeSecurityGroups
       └─ print SGReport as structured table or JSON
```

### kdiag eks node

```
cmd/eks/node.go
  │
  ├─ k8s.NewClient(configFlags)
  │
  ├─ requireEKS(host)
  │
  ├─ newEC2Client(ctx, host)
  │
  ├─ CoreV1.Nodes().List()
  │    └─ k8s.DetectNodeComputeType(node) per node:
  │         Fargate  → skip
  │         AutoMode → eligible with note
  │         Managed  → eligible
  │
  ├─ aws.GetInstanceTypeLimits(uniqueTypes)   ← DescribeInstanceTypes (batched)
  │
  └─ per node: aws.ListNodeENIs(instanceID)   ← DescribeNetworkInterfaces
       └─ calculate utilization
       │    < 70% → OK
       │    70-84% → WARNING
       │    >= 85% → EXHAUSTED
       └─ print NodeReport as table or JSON
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
| `github.com/aws/aws-sdk-go-v2` | v1.x | AWS SDK for EC2 API calls in `pkg/aws` and `cmd/eks` |
