# kdiag capture

Capture network traffic from a pod using tcpdump, with optional BPF filtering and pcap file output.

## Synopsis

```
kdiag capture <pod-name> [flags]
```

## Description

`kdiag capture` injects an ephemeral container running tcpdump into the target pod. Because the ephemeral container shares the pod's network namespace, tcpdump sees the same traffic the application sees.

By default, output is streamed to stdout in human-readable tcpdump format. With `--write`, raw pcap data is written to a local file for analysis in Wireshark or tshark.

Capture runs until you press Ctrl-C, the `--count` packet limit is reached, or the `--duration` timeout expires.

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--filter` | `-f` | — | BPF filter expression passed to tcpdump |
| `--write` | `-w` | — | Write raw pcap data to this local file path |
| `--interface` | `-i` | `any` | Network interface to capture on |
| `--count` | `-c` | `0` (unlimited) | Stop after this many packets |
| `--duration` | `-d` | `0` (unlimited) | Stop capture after this duration (e.g. `30s`, `2m`) |

All [global flags](../README.md#global-flags) apply (`--namespace`, `--image`, `--timeout`, etc.).

Note: `--timeout` controls how long kdiag waits for the ephemeral container to **start** (default 30s). It does not limit capture duration — use `--duration` for that.

## Examples

**Stream all traffic from a pod:**

```bash
kdiag capture my-pod
```

**Capture only HTTPS traffic:**

```bash
kdiag capture my-pod --filter "port 443"
```

**Capture traffic to/from a specific IP:**

```bash
kdiag capture my-pod --filter "host 10.0.2.15"
```

**Capture the first 100 packets and stop:**

```bash
kdiag capture my-pod --count 100
```

**Capture for 30 seconds:**

```bash
kdiag capture my-pod --duration 30s
```

**Save a 60-second capture to a pcap file:**

```bash
kdiag capture my-pod -w /tmp/trace.pcap --duration 60s
```

**Open the pcap directly in Wireshark (macOS):**

```bash
kdiag capture my-pod -w /tmp/trace.pcap --duration 30s && open /tmp/trace.pcap
```

**Capture on a specific interface:**

```bash
kdiag capture my-pod --interface eth0
```

**Verbose mode:**

```bash
kdiag capture my-pod -f "port 80" -v
# [verbose] pod: default/my-pod
# [verbose] tcpdump command: [tcpdump -i any -l port 80]
# [verbose] ephemeral container name: kdiag-xk2mp
```

## BPF Filter Reference

The `--filter` value is passed directly to tcpdump as a BPF expression. Filters must be 1024 characters or fewer. Common patterns:

| Filter | Captures |
|--------|----------|
| `port 443` | HTTPS traffic |
| `port 53` | DNS queries and responses |
| `host 10.0.2.15` | Traffic to/from a specific IP |
| `tcp` | TCP traffic only |
| `udp` | UDP traffic only |
| `not port 22` | Everything except SSH |
| `port 80 or port 443` | HTTP and HTTPS |

Full BPF syntax reference: `man pcap-filter` or the [tcpdump manual](https://www.tcpdump.org/manpages/pcap-filter.7.html).

## Signal Handling

Pressing Ctrl-C (SIGINT) or sending SIGTERM cancels the capture context. kdiag prints "Capture interrupted." to stderr. If `--write` was set, any partial data already written to the file is preserved:

```
Capture interrupted.
Partial capture written to /tmp/trace.pcap
```

When the `--duration` limit is reached, kdiag exits cleanly with "Capture complete."

## Capture Lifecycle

1. kdiag validates the `--write` output directory exists (before any Kubernetes work).
2. RBAC is checked — requires `update pods/ephemeralcontainers` and `create pods/attach`.
3. An ephemeral container running the tcpdump command is injected into the pod.
4. kdiag waits (up to `--timeout`) for the container to reach Running.
5. kdiag attaches to the container's stdout/stderr streams:
   - Without `--write`: tcpdump output streams to your terminal.
   - With `--write`: raw pcap bytes stream to the local file.
6. On exit, the ephemeral container is left for the kubelet to garbage-collect.

## RBAC Requirements

`kdiag capture` requires:
- `update pods/ephemeralcontainers` in the target namespace
- `create pods/attach` in the target namespace

If any permission is missing, kdiag prints the denied permissions and remediation steps. See [RBAC requirements](../README.md#rbac-requirements).

## Troubleshooting

**"output directory does not exist"**

The parent directory of your `--write` path does not exist. Create it first:

```bash
mkdir -p /tmp/captures
kdiag capture my-pod -w /tmp/captures/trace.pcap
```

**"output file already exists and will be overwritten"**

This is a warning, not an error. The existing file will be replaced. Rename or move it first if you need to keep it.

**"failed waiting for capture container"**

The ephemeral container did not start within `--timeout`. Increase the timeout or check whether the node can pull the debug image:

```bash
kdiag capture my-pod --timeout 2m
```

**No output after the container starts**

The BPF filter may be too restrictive, or no matching traffic is flowing. Try removing `--filter` to confirm traffic is present.
