# Design: Kubernetes Coverage Gaps

Date: 2026-03-07

## Overview

Fill diagnostic gaps identified by comparing kdiag against Kubernetes architecture fundamentals. Six changes across three areas: flag rename, new EKS command, new Ingress command, and enhanced `diagnose` checks.

## 1. Rename `--aws-profile` to `--profile` and `--aws-region` to `--region`

**Scope:** `eks` subcommand only (not promoted to root).

**Rationale:** Aligns with Terraform naming conventions. Users pass `--profile` habitually; `--aws-profile` is an extra mental translation.

**Changes:**
- `cmd/eks/eks.go`: Rename flag registrations from `aws-profile` to `profile` and `aws-region` to `region`
- `cmd/eks/eks.go`: Rename the Go variables `awsProfile` and `awsRegion` (optional, for consistency)
- `skill/SKILL.md`: Update all references from `--aws-profile`/`--aws-region` to `--profile`/`--region`. Add guidance for the skill to ask the user which AWS profile to use when running EKS commands.
- `README.md`: No changes needed (doesn't reference these flags by name)

**Breaking change:** Yes — users passing `--aws-profile` or `--aws-region` will get an error. Acceptable since kdiag is pre-1.0.

## 2. `kdiag eks endpoint` — VPC Endpoint Checks

**Purpose:** Verify that traffic to AWS services stays on the private network and doesn't traverse the internet.

**Services checked:** STS, EC2, ECR (api + dkr), S3, CloudWatch Logs, EKS API.

**Two-phase check:**

### Phase 1: DNS resolution (always runs, no extra IAM)
For each service, resolve the regional endpoint (e.g., `ec2.us-east-1.amazonaws.com`) and classify the returned IP:
- Private IP (10.x, 172.16-31.x, 192.168.x) = VPC endpoint exists
- Public IP = traffic goes over internet

### Phase 2: `DescribeVpcEndpoints` (runs when permissions allow)
- Call `ec2:DescribeVpcEndpoints` filtered by service name
- Report: endpoint ID, type (Interface/Gateway), state, route tables (for Gateway endpoints)
- If the call fails with AccessDenied, degrade gracefully — report DNS results only with a note

### EKS API private access
- Parse the cluster endpoint from kubeconfig
- Resolve DNS — if it returns a private IP, private access is enabled
- Alternatively: call `eks:DescribeCluster` if permissions allow, check `resourcesVpcConfig.endpointPrivateAccess`

### Output

Table format:
```
SERVICE          DNS_RESULT   ENDPOINT_TYPE   ENDPOINT_ID          STATE
sts              private      Interface       vpce-0abc123...      available
ec2              public       -               -                    -
ecr.api          private      Interface       vpce-0def456...      available
ecr.dkr          private      Interface       vpce-0def456...      available
s3               private      Gateway         vpce-0ghi789...      available
logs             public       -               -                    -
eks-api          private      -               -                    -
```

Severity in diagnose context:
- All private = pass
- Some public = warn (with list of missing endpoints)

### New files
- `cmd/eks/endpoint.go` — command implementation
- `pkg/aws/endpoint.go` — DNS resolution + DescribeVpcEndpoints logic
- `pkg/aws/endpoint_test.go` — unit tests

### IAM permissions (additive)
- `ec2:DescribeVpcEndpoints` (optional, for Phase 2)
- `eks:DescribeCluster` (optional, for EKS API private access detail)

## 3. ConfigMap/Secret Missing-Reference Check in `diagnose`

**Purpose:** Detect pods failing because they reference ConfigMaps or Secrets that don't exist.

**Scan points in pod spec:**
- `spec.containers[*].env[*].valueFrom.configMapKeyRef`
- `spec.containers[*].env[*].valueFrom.secretKeyRef`
- `spec.containers[*].envFrom[*].configMapRef`
- `spec.containers[*].envFrom[*].secretRef`
- `spec.volumes[*].configMap`
- `spec.volumes[*].secret`
- `spec.volumes[*].projected.sources[*].configMap`
- `spec.volumes[*].projected.sources[*].secret`
- Same for `initContainers` and `ephemeralContainers`

**Logic:**
1. Collect all referenced ConfigMap and Secret names from the pod spec
2. For each, attempt a `GET` — if 404, it's missing
3. Respect the `optional` field: if `optional: true`, a missing ref is not an error

**Severity:**
- All refs exist = pass ("N configmap(s), M secret(s) verified")
- Missing ref with `optional: false` (or unset) = fail ("configmap/foo not found")
- Missing ref with `optional: true` = warn ("optional configmap/bar not found")
- API error (e.g., RBAC) = error

**RBAC required:** `get` on `configmaps` and `secrets` in the pod's namespace.

**Changes:**
- `cmd/diagnose.go`: Add `refs` check after `inspect` check, before `dns`
- New helper function `checkPodRefs(ctx, client, pod)` in `cmd/diagnose.go` or a small `cmd/refs.go`

## 4. `kdiag ingress <name>` — Ingress Diagnostics

**Purpose:** Inspect an Ingress resource: validate rules, verify backend Services exist and have endpoints, check TLS secrets, detect controller type.

### Checks performed

1. **Ingress exists** — fetch the named Ingress
2. **Backend Services exist** — for each rule path backend, verify the Service exists
3. **Endpoints exist** — for each backend Service, verify it has at least one ready endpoint
4. **TLS Secrets exist** — for each `spec.tls[*].secretName`, verify the Secret exists
5. **Controller detection** — check `ingressClassName` or `kubernetes.io/ingress.class` annotation:
   - `alb`: check aws-load-balancer-controller pods in kube-system
   - `nginx`: check ingress-nginx-controller pods
   - Other/unknown: skip controller health check
6. **Controller health** — if detected, verify controller pods are Running and Ready

### Output

```
Ingress: my-ingress
Namespace: default
Class: alb

Rules:
  HOST              PATH    SERVICE         PORT   ENDPOINTS
  app.example.com   /       my-service      80     3 ready
  app.example.com   /api    api-service     8080   0 ready  <-- WARN

TLS:
  SECRET              HOSTS                  STATUS
  app-tls             app.example.com        found
  missing-tls         api.example.com        NOT FOUND  <-- FAIL

Controller: aws-load-balancer-controller (2/2 pods ready)
```

### Ingress check in `diagnose`

When diagnosing a pod:
1. Get the pod's labels
2. List all Services in the namespace, find ones whose selector matches the pod
3. List all Ingresses in the namespace, find ones that reference those Services
4. If any Ingress found, run a lightweight check (backend endpoints exist, TLS secrets exist)
5. Report as a row: pass/warn/fail

**Severity in diagnose:**
- No Ingresses reference this pod = pass ("no ingress references found")
- Ingresses found, all backends healthy = pass ("N ingress(es) verified")
- Missing endpoints or TLS secrets = warn/fail

### New files
- `cmd/ingress.go` — standalone command + diagnose helper
- Supported resource types: `ingress/<name>` added to `parseResourceArg` is NOT needed since this is a separate command, not part of `inspect`

### RBAC required
- `get`, `list` on `ingresses` (networking.k8s.io)
- `get` on `services`, `endpoints`, `secrets`

## 5. Skill Updates

### Add `--profile` prompting
The skill should ask "Which AWS profile should I use?" when running EKS commands, and pass `--profile <name>` if the user specifies one.

### New commands in quick reference table
- `kdiag ingress <name>` — Inspect Ingress rules, backends, TLS, and controller health
- `kdiag eks endpoint` — Check VPC endpoints for AWS service connectivity

### New playbook sections
- **Ingress Issues** playbook: `ingress <name>` -> check backends -> check TLS -> check controller
- **EKS Endpoint/Connectivity** playbook: `eks endpoint` -> flag public services -> recommend VPC endpoints

### Update existing playbook
- EKS-Specific Issues section: add `eks endpoint` command reference
- Pod Not Running playbook: mention that `diagnose` now checks ConfigMap/Secret refs

## Implementation Order

1. Flag rename (`--profile`, `--region`) — smallest, unblocks skill update
2. ConfigMap/Secret refs check in `diagnose` — self-contained, high value
3. `kdiag ingress <name>` + diagnose integration
4. `kdiag eks endpoint`
5. Skill updates (after all commands exist)
