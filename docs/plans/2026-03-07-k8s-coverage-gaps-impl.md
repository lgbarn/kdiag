# Kubernetes Coverage Gaps Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use shipyard:shipyard-executing-plans to implement this plan task-by-task.

**Goal:** Fill kdiag's Kubernetes coverage gaps: rename AWS flags, add ConfigMap/Secret ref checks, Ingress diagnostics, and VPC endpoint verification.

**Architecture:** Five sequential phases. Phase 1 is a flag rename. Phase 2 adds a diagnose check by scanning pod specs for missing ConfigMap/Secret refs. Phase 3 adds a standalone `kdiag ingress` command plus a diagnose integration. Phase 4 adds `kdiag eks endpoint` with DNS-based and API-based VPC endpoint checks. Phase 5 updates the Claude Code skill.

**Tech Stack:** Go 1.25, cobra, k8s.io/client-go, aws-sdk-go-v2

---

## Phase 1: Rename AWS Flags

### Task 1: Rename `--aws-profile` to `--profile` and `--aws-region` to `--region`

**Files:**
- Modify: `cmd/eks/eks.go:50-51` (flag registration)
- Modify: `cmd/diagnose.go:142` (pass profile to NewEC2Client)

**Step 1: Rename flag registrations in eks.go**

In `cmd/eks/eks.go`, change line 50-51 from:
```go
EksCmd.PersistentFlags().StringVar(&awsProfile, "aws-profile", "", "AWS shared config profile to use")
EksCmd.PersistentFlags().StringVar(&awsRegion, "aws-region", "", "AWS region override (auto-detected from EKS endpoint when omitted)")
```
to:
```go
EksCmd.PersistentFlags().StringVar(&awsProfile, "profile", "", "AWS shared config profile to use (like terraform -profile)")
EksCmd.PersistentFlags().StringVar(&awsRegion, "region", "", "AWS region override (auto-detected from EKS endpoint when omitted)")
```

**Step 2: Expose the profile getter for diagnose.go**

Add a public getter to `cmd/eks/eks.go` after the existing `isVerbose()` function:
```go
// GetAWSProfile returns the current --profile flag value.
func GetAWSProfile() string { return awsProfile }
```

**Step 3: Wire profile into diagnose.go**

In `cmd/diagnose.go:142`, change:
```go
ec2Client, ec2Err := awspkg.NewEC2Client(ctx, region, "")
```
to:
```go
ec2Client, ec2Err := awspkg.NewEC2Client(ctx, region, eks.GetAWSProfile())
```

**Step 4: Build and verify**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build, no errors.

**Step 5: Run existing tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./...`
Expected: All tests pass.

**Step 6: Commit**

```bash
git add cmd/eks/eks.go cmd/diagnose.go
git commit -m "$(cat <<'EOF'
feat: rename --aws-profile to --profile and --aws-region to --region

Aligns with Terraform flag conventions. Also wires the profile flag
into the diagnose command so EKS checks use the specified profile.
EOF
)"
```

---

## Phase 2: ConfigMap/Secret Missing-Reference Check in Diagnose

### Task 2: Add ref extraction helper with tests

**Files:**
- Create: `cmd/refs.go`
- Create: `cmd/refs_test.go`

**Step 1: Write the failing test**

Create `cmd/refs_test.go`:
```go
package cmd

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestExtractPodRefs_EnvValueFrom(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "DB_HOST",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "db-config"},
								},
							},
						},
						{
							Name: "DB_PASS",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "db-secret"},
								},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	cmFound := false
	secFound := false
	for _, r := range refs {
		if r.Kind == "ConfigMap" && r.Name == "db-config" && !r.Optional {
			cmFound = true
		}
		if r.Kind == "Secret" && r.Name == "db-secret" && !r.Optional {
			secFound = true
		}
	}
	if !cmFound {
		t.Error("expected ConfigMap ref 'db-config' not found")
	}
	if !secFound {
		t.Error("expected Secret ref 'db-secret' not found")
	}
}

func TestExtractPodRefs_EnvFrom(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
						}},
						{SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-secret"},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_Volumes(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "config-vol",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "vol-cm"},
						},
					},
				},
				{
					Name: "secret-vol",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "vol-secret",
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_Optional(t *testing.T) {
	optional := true
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "OPT",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "opt-cm"},
									Optional:             &optional,
								},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !refs[0].Optional {
		t.Error("expected ref to be optional")
	}
}

func TestExtractPodRefs_Projected(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "projected-vol",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{ConfigMap: &corev1.ConfigMapProjection{
									LocalObjectReference: corev1.LocalObjectReference{Name: "proj-cm"},
								}},
								{Secret: &corev1.SecretProjection{
									LocalObjectReference: corev1.LocalObjectReference{Name: "proj-secret"},
								}},
							},
						},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestExtractPodRefs_InitContainers(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "init-cm"},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Name != "init-cm" {
		t.Errorf("expected 'init-cm', got %q", refs[0].Name)
	}
}

func TestExtractPodRefs_Dedup(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{Name: "A", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "same-cm"},
							},
						}},
						{Name: "B", ValueFrom: &corev1.EnvVarSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "same-cm"},
							},
						}},
					},
				},
			},
		},
	}

	refs := extractPodRefs(pod)

	if len(refs) != 1 {
		t.Fatalf("expected 1 deduped ref, got %d", len(refs))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./cmd/ -run TestExtractPodRefs -v`
Expected: FAIL — `extractPodRefs` undefined.

**Step 3: Write the implementation**

Create `cmd/refs.go`:
```go
package cmd

import (
	corev1 "k8s.io/api/core/v1"
)

// podRef represents a ConfigMap or Secret referenced by a pod spec.
type podRef struct {
	Kind     string // "ConfigMap" or "Secret"
	Name     string
	Optional bool
}

// extractPodRefs scans a pod spec for all ConfigMap and Secret references
// across containers, initContainers, ephemeralContainers, and volumes.
// Returns a deduplicated slice.
func extractPodRefs(pod *corev1.Pod) []podRef {
	seen := map[string]bool{} // "Kind/Name" -> true
	var refs []podRef

	add := func(kind, name string, optional bool) {
		if name == "" {
			return
		}
		key := kind + "/" + name
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, podRef{Kind: kind, Name: name, Optional: optional})
	}

	// Scan containers of all types.
	allContainers := make([]corev1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	allContainers = append(allContainers, pod.Spec.Containers...)
	allContainers = append(allContainers, pod.Spec.InitContainers...)

	for _, c := range allContainers {
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil {
				add("ConfigMap", ref.Name, ref.Optional != nil && *ref.Optional)
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil {
				add("Secret", ref.Name, ref.Optional != nil && *ref.Optional)
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				opt := ef.ConfigMapRef.Optional != nil && *ef.ConfigMapRef.Optional
				add("ConfigMap", ef.ConfigMapRef.Name, opt)
			}
			if ef.SecretRef != nil {
				opt := ef.SecretRef.Optional != nil && *ef.SecretRef.Optional
				add("Secret", ef.SecretRef.Name, opt)
			}
		}
	}

	// Scan ephemeral containers (different type, same env fields).
	for _, c := range pod.Spec.EphemeralContainers {
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil {
				add("ConfigMap", ref.Name, ref.Optional != nil && *ref.Optional)
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil {
				add("Secret", ref.Name, ref.Optional != nil && *ref.Optional)
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				opt := ef.ConfigMapRef.Optional != nil && *ef.ConfigMapRef.Optional
				add("ConfigMap", ef.ConfigMapRef.Name, opt)
			}
			if ef.SecretRef != nil {
				opt := ef.SecretRef.Optional != nil && *ef.SecretRef.Optional
				add("Secret", ef.SecretRef.Name, opt)
			}
		}
	}

	// Scan volumes.
	for _, v := range pod.Spec.Volumes {
		if v.ConfigMap != nil {
			opt := v.ConfigMap.Optional != nil && *v.ConfigMap.Optional
			add("ConfigMap", v.ConfigMap.Name, opt)
		}
		if v.Secret != nil {
			opt := v.Secret.Optional != nil && *v.Secret.Optional
			add("Secret", v.Secret.SecretName, opt)
		}
		if v.Projected != nil {
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					opt := src.ConfigMap.Optional != nil && *src.ConfigMap.Optional
					add("ConfigMap", src.ConfigMap.Name, opt)
				}
				if src.Secret != nil {
					opt := src.Secret.Optional != nil && *src.Secret.Optional
					add("Secret", src.Secret.Name, opt)
				}
			}
		}
	}

	return refs
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./cmd/ -run TestExtractPodRefs -v`
Expected: All 7 tests PASS.

**Step 5: Commit**

```bash
git add cmd/refs.go cmd/refs_test.go
git commit -m "$(cat <<'EOF'
feat: add ConfigMap/Secret reference extraction from pod specs

Scans containers, initContainers, ephemeralContainers, envFrom,
env.valueFrom, volumes, and projected volumes. Deduplicates and
tracks the optional flag.
EOF
)"
```

### Task 3: Wire refs check into diagnose command

**Files:**
- Modify: `cmd/diagnose.go` (add refs check between inspect and dns checks)
- Modify: `cmd/diagnose_types.go` (add refsSeverity helper)

**Step 1: Add the refsSeverity helper**

Append to `cmd/diagnose_types.go` after the `sgSeverity` function (after line 145):
```go
// refsSeverity derives a severity and summary from a refs check result.
func refsSeverity(missing []podRef, optionalMissing []podRef, total int) (severity, summary string) {
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, r := range missing {
			names = append(names, strings.ToLower(r.Kind)+"/"+r.Name)
		}
		return SeverityFail, fmt.Sprintf("missing: %s", strings.Join(names, ", "))
	}
	if len(optionalMissing) > 0 {
		names := make([]string, 0, len(optionalMissing))
		for _, r := range optionalMissing {
			names = append(names, strings.ToLower(r.Kind)+"/"+r.Name)
		}
		return SeverityWarn, fmt.Sprintf("optional missing: %s", strings.Join(names, ", "))
	}
	return SeverityPass, fmt.Sprintf("%d configmap/secret ref(s) verified", total)
}
```

**Step 2: Add the refs check in diagnose.go**

In `cmd/diagnose.go`, after the inspect check block (after line 80) and before the DNS check (line 82), insert:
```go
	// Refs check: verify ConfigMap/Secret references exist.
	if pod != nil {
		refs := extractPodRefs(pod)
		if len(refs) == 0 {
			report.Checks = append(report.Checks, DiagnoseCheckResult{
				Name: "refs", Severity: SeverityPass, Summary: "no configmap/secret refs",
			})
		} else {
			var missing, optionalMissing []podRef
			for _, ref := range refs {
				var err error
				if ref.Kind == "ConfigMap" {
					_, err = client.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
				} else {
					_, err = client.Clientset.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
				}
				if err != nil {
					if apierrors.IsNotFound(err) {
						if ref.Optional {
							optionalMissing = append(optionalMissing, ref)
						} else {
							missing = append(missing, ref)
						}
					}
					// Ignore non-404 errors (RBAC etc.) — don't fail the whole check.
				}
			}
			sev, sum := refsSeverity(missing, optionalMissing, len(refs))
			report.Checks = append(report.Checks, DiagnoseCheckResult{
				Name: "refs", Severity: sev, Summary: sum,
			})
		}
	}
```

Note: The `pod` variable is already fetched at line 100 of diagnose.go for the netpol check. Move the pod fetch earlier (before the refs check) so both refs and netpol can use it. Specifically, move lines 100-101 (`pod, podErr := ...`) to just after line 80 (after the inspect check), so the refs check can use `pod`.

Also add the `apierrors` import if not already present (it is — line 11 currently imports it via the netpol section, but verify it's available at the new location).

**Step 3: Build and verify**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build.

**Step 4: Run all tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./...`
Expected: All tests pass.

**Step 5: Commit**

```bash
git add cmd/diagnose.go cmd/diagnose_types.go
git commit -m "$(cat <<'EOF'
feat: add ConfigMap/Secret ref check to diagnose command

Scans the pod spec for configMapKeyRef, secretKeyRef, envFrom, and
volume references. Verifies each exists in the namespace. Reports
fail for missing required refs, warn for missing optional refs.
EOF
)"
```

---

## Phase 3: Ingress Diagnostics

### Task 4: Add standalone `kdiag ingress` command

**Files:**
- Create: `cmd/ingress.go`
- Create: `cmd/ingress_test.go`

**Step 1: Write the test for Ingress result building**

Create `cmd/ingress_test.go`:
```go
package cmd

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDetectIngressController(t *testing.T) {
	tests := []struct {
		name       string
		className  *string
		annotation string
		want       string
	}{
		{"alb class", strPtr("alb"), "", "alb"},
		{"nginx class", strPtr("nginx"), "", "nginx"},
		{"alb annotation", nil, "alb", "alb"},
		{"nginx annotation", nil, "nginx", "nginx"},
		{"unknown", strPtr("traefik"), "", "traefik"},
		{"no class", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ingress := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: tt.className,
				},
			}
			if tt.annotation != "" {
				ingress.Annotations["kubernetes.io/ingress.class"] = tt.annotation
			}
			got := detectIngressController(ingress)
			if got != tt.want {
				t.Errorf("detectIngressController() = %q, want %q", got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./cmd/ -run TestDetectIngressController -v`
Expected: FAIL — `detectIngressController` undefined.

**Step 3: Write the ingress command implementation**

Create `cmd/ingress.go`:
```go
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// IngressResult holds the structured output of an ingress inspection.
type IngressResult struct {
	Name       string              `json:"name"`
	Namespace  string              `json:"namespace"`
	Class      string              `json:"class"`
	Controller string              `json:"controller"`
	Rules      []IngressRuleResult `json:"rules"`
	TLS        []IngressTLSResult  `json:"tls"`
	CtrlHealth string              `json:"controller_health,omitempty"`
}

// IngressRuleResult holds the check result for a single ingress rule path.
type IngressRuleResult struct {
	Host           string `json:"host"`
	Path           string `json:"path"`
	ServiceName    string `json:"service_name"`
	ServicePort    string `json:"service_port"`
	ServiceExists  bool   `json:"service_exists"`
	EndpointsReady int    `json:"endpoints_ready"`
}

// IngressTLSResult holds the check result for a TLS secret reference.
type IngressTLSResult struct {
	SecretName string   `json:"secret_name"`
	Hosts      []string `json:"hosts"`
	Exists     bool     `json:"exists"`
}

var ingressCmd = &cobra.Command{
	Use:   "ingress <name>",
	Short: "Inspect Ingress rules, backends, TLS secrets, and controller health",
	Args:  cobra.ExactArgs(1),
	RunE:  runIngress,
}

func init() {
	rootCmd.AddCommand(ingressCmd)
}

func runIngress(cmd *cobra.Command, args []string) error {
	name := args[0]

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}
	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	ingress, err := client.Clientset.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("ingress %q not found in namespace %q", name, namespace)
		}
		return fmt.Errorf("failed to get ingress %q: %w", name, err)
	}

	controller := detectIngressController(ingress)
	class := ""
	if ingress.Spec.IngressClassName != nil {
		class = *ingress.Spec.IngressClassName
	} else if v, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		class = v
	}

	result := IngressResult{
		Name:       name,
		Namespace:  namespace,
		Class:      class,
		Controller: controller,
	}

	// Check rules and backends.
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			rr := IngressRuleResult{
				Host: rule.Host,
				Path: path.Path,
			}
			if path.Backend.Service != nil {
				rr.ServiceName = path.Backend.Service.Name
				if path.Backend.Service.Port.Name != "" {
					rr.ServicePort = path.Backend.Service.Port.Name
				} else {
					rr.ServicePort = fmt.Sprintf("%d", path.Backend.Service.Port.Number)
				}
				// Check service exists.
				_, svcErr := client.Clientset.CoreV1().Services(namespace).Get(ctx, rr.ServiceName, metav1.GetOptions{})
				rr.ServiceExists = svcErr == nil

				// Check endpoints.
				if rr.ServiceExists {
					ep, epErr := client.Clientset.CoreV1().Endpoints(namespace).Get(ctx, rr.ServiceName, metav1.GetOptions{})
					if epErr == nil {
						rr.EndpointsReady = countReadyEndpoints(ep)
					}
				}
			}
			result.Rules = append(result.Rules, rr)
		}
	}

	// Check TLS secrets.
	for _, tls := range ingress.Spec.TLS {
		tr := IngressTLSResult{
			SecretName: tls.SecretName,
			Hosts:      tls.Hosts,
		}
		if tls.SecretName != "" {
			_, secErr := client.Clientset.CoreV1().Secrets(namespace).Get(ctx, tls.SecretName, metav1.GetOptions{})
			tr.Exists = secErr == nil
		}
		result.TLS = append(result.TLS, tr)
	}

	// Check controller health.
	result.CtrlHealth = checkControllerHealth(ctx, client, controller)

	// Output.
	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	return printIngressTable(result)
}

// detectIngressController returns the controller type from the Ingress spec.
func detectIngressController(ingress *networkingv1.Ingress) string {
	if ingress.Spec.IngressClassName != nil {
		return *ingress.Spec.IngressClassName
	}
	if v, ok := ingress.Annotations["kubernetes.io/ingress.class"]; ok {
		return v
	}
	return ""
}

// countReadyEndpoints counts the number of ready addresses across all subsets.
func countReadyEndpoints(ep *corev1.Endpoints) int {
	count := 0
	for _, subset := range ep.Subsets {
		count += len(subset.Addresses)
	}
	return count
}

// checkControllerHealth checks if known ingress controller pods are healthy.
func checkControllerHealth(ctx context.Context, client *k8s.Client, controller string) string {
	var labelSelector, namespace string

	switch controller {
	case "alb":
		labelSelector = "app.kubernetes.io/name=aws-load-balancer-controller"
		namespace = "kube-system"
	case "nginx":
		labelSelector = "app.kubernetes.io/name=ingress-nginx"
		namespace = "ingress-nginx"
	default:
		return ""
	}

	pods, err := client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Sprintf("failed to list %s controller pods: %v", controller, err)
	}

	if len(pods.Items) == 0 {
		// Try kube-system for nginx as fallback.
		if controller == "nginx" {
			pods, err = client.Clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil || len(pods.Items) == 0 {
				return "no controller pods found"
			}
		} else {
			return "no controller pods found"
		}
	}

	ready := 0
	for _, pod := range pods.Items {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	return fmt.Sprintf("%d/%d pods ready", ready, len(pods.Items))
}

func printIngressTable(result IngressResult) error {
	fmt.Fprintf(os.Stdout, "Ingress: %s\n", result.Name)
	fmt.Fprintf(os.Stdout, "Namespace: %s\n", result.Namespace)
	if result.Class != "" {
		fmt.Fprintf(os.Stdout, "Class: %s\n", result.Class)
	}
	fmt.Fprintln(os.Stdout)

	// Rules table.
	if len(result.Rules) > 0 {
		fmt.Fprintln(os.Stdout, "Rules:")
		p := output.NewTablePrinter(os.Stdout)
		p.PrintHeader("  HOST", "PATH", "SERVICE", "PORT", "SVC EXISTS", "ENDPOINTS")
		for _, r := range result.Rules {
			exists := "yes"
			if !r.ServiceExists {
				exists = "NOT FOUND"
			}
			ep := fmt.Sprintf("%d ready", r.EndpointsReady)
			if !r.ServiceExists {
				ep = "-"
			} else if r.EndpointsReady == 0 {
				ep = "0 ready"
			}
			p.PrintRow("  "+r.Host, r.Path, r.ServiceName, r.ServicePort, exists, ep)
		}
		if err := p.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout)
	}

	// TLS table.
	if len(result.TLS) > 0 {
		fmt.Fprintln(os.Stdout, "TLS:")
		p := output.NewTablePrinter(os.Stdout)
		p.PrintHeader("  SECRET", "HOSTS", "STATUS")
		for _, t := range result.TLS {
			status := "found"
			if !t.Exists {
				status = "NOT FOUND"
			}
			p.PrintRow("  "+t.SecretName, strings.Join(t.Hosts, ", "), status)
		}
		if err := p.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout)
	}

	// Controller health.
	if result.CtrlHealth != "" {
		fmt.Fprintf(os.Stdout, "Controller (%s): %s\n", result.Controller, result.CtrlHealth)
	}

	return nil
}
```

**Step 4: Run tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./cmd/ -run TestDetectIngressController -v`
Expected: PASS.

**Step 5: Build**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build.

**Step 6: Commit**

```bash
git add cmd/ingress.go cmd/ingress_test.go
git commit -m "$(cat <<'EOF'
feat: add kdiag ingress command

Inspects Ingress resources: validates backend Services exist and have
endpoints, checks TLS secrets, detects controller type (ALB/NGINX),
and reports controller pod health.
EOF
)"
```

### Task 5: Add ingress check to diagnose command

**Files:**
- Modify: `cmd/diagnose.go` (add ingress check)
- Modify: `cmd/diagnose_types.go` (add ingressSeverity helper)

**Step 1: Add the ingressSeverity helper**

Append to `cmd/diagnose_types.go`:
```go
// ingressSeverity derives a severity from ingress check results found during diagnose.
func ingressSeverity(rules []IngressRuleResult, tlsList []IngressTLSResult) (severity, summary string) {
	if len(rules) == 0 {
		return SeverityPass, "no ingress references found"
	}

	var issues []string
	for _, r := range rules {
		if !r.ServiceExists {
			issues = append(issues, fmt.Sprintf("service %q not found", r.ServiceName))
		} else if r.EndpointsReady == 0 {
			issues = append(issues, fmt.Sprintf("service %q has 0 endpoints", r.ServiceName))
		}
	}
	for _, t := range tlsList {
		if !t.Exists && t.SecretName != "" {
			issues = append(issues, fmt.Sprintf("TLS secret %q not found", t.SecretName))
		}
	}

	if len(issues) > 0 {
		return SeverityFail, strings.Join(issues, "; ")
	}
	return SeverityPass, fmt.Sprintf("%d ingress rule(s) verified", len(rules))
}
```

**Step 2: Add the ingress check in diagnose.go**

In `cmd/diagnose.go`, after the netpol check block and before the EKS-specific checks, insert:
```go
	// Ingress check: find Ingresses routing to this pod's Services.
	if pod != nil {
		ingRules, ingTLS := findIngressesForPod(ctx, client, namespace, pod)
		sev, sum := ingressSeverity(ingRules, ingTLS)
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "ingress", Severity: sev, Summary: sum,
		})
	}
```

Add the helper function `findIngressesForPod` to `cmd/ingress.go`:
```go
// findIngressesForPod finds Ingresses that route to Services selecting this pod.
func findIngressesForPod(ctx context.Context, client *k8s.Client, namespace string, pod *corev1.Pod) ([]IngressRuleResult, []IngressTLSResult) {
	// Find services that select this pod.
	svcList, err := client.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}

	podSvcNames := map[string]bool{}
	for _, svc := range svcList.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		match := true
		for k, v := range svc.Spec.Selector {
			if pod.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			podSvcNames[svc.Name] = true
		}
	}

	if len(podSvcNames) == 0 {
		return nil, nil
	}

	// Find ingresses referencing those services.
	ingList, err := client.Clientset.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}

	var rules []IngressRuleResult
	var tlsResults []IngressTLSResult
	seenTLS := map[string]bool{}

	for _, ing := range ingList.Items {
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service == nil {
					continue
				}
				if !podSvcNames[path.Backend.Service.Name] {
					continue
				}
				rr := IngressRuleResult{
					Host:        rule.Host,
					Path:        path.Path,
					ServiceName: path.Backend.Service.Name,
					ServiceExists: true, // we already know it exists
				}
				if path.Backend.Service.Port.Name != "" {
					rr.ServicePort = path.Backend.Service.Port.Name
				} else {
					rr.ServicePort = fmt.Sprintf("%d", path.Backend.Service.Port.Number)
				}
				ep, epErr := client.Clientset.CoreV1().Endpoints(namespace).Get(ctx, rr.ServiceName, metav1.GetOptions{})
				if epErr == nil {
					rr.EndpointsReady = countReadyEndpoints(ep)
				}
				rules = append(rules, rr)
			}
		}

		// Check TLS for matching ingresses.
		for _, tls := range ing.Spec.TLS {
			if seenTLS[tls.SecretName] {
				continue
			}
			seenTLS[tls.SecretName] = true
			tr := IngressTLSResult{
				SecretName: tls.SecretName,
				Hosts:      tls.Hosts,
			}
			if tls.SecretName != "" {
				_, secErr := client.Clientset.CoreV1().Secrets(namespace).Get(ctx, tls.SecretName, metav1.GetOptions{})
				tr.Exists = secErr == nil
			}
			tlsResults = append(tlsResults, tr)
		}
	}

	return rules, tlsResults
}
```

**Step 3: Build and verify**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build.

**Step 4: Run all tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./...`
Expected: All tests pass.

**Step 5: Commit**

```bash
git add cmd/ingress.go cmd/diagnose.go cmd/diagnose_types.go
git commit -m "$(cat <<'EOF'
feat: add ingress check to diagnose command

Finds Ingresses routing to Services that select the diagnosed pod.
Verifies backend endpoints exist and TLS secrets are present.
EOF
)"
```

---

## Phase 4: VPC Endpoint Checks

### Task 6: Add EC2API.DescribeVpcEndpoints to interface and mock

**Files:**
- Modify: `pkg/aws/ec2iface.go` (add DescribeVpcEndpoints method)
- Modify: `pkg/aws/ec2iface_mock_test.go` (add mock method)

**Step 1: Add DescribeVpcEndpoints to EC2API interface**

In `pkg/aws/ec2iface.go`, add to the interface:
```go
DescribeVpcEndpoints(ctx context.Context, params *ec2.DescribeVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error)
```

**Step 2: Add mock method**

In `pkg/aws/ec2iface_mock_test.go`, add the field and method:
```go
// Add field to mockEC2API struct:
describeVpcEndpoints func(ctx context.Context, params *ec2.DescribeVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error)

// Add method:
func (m *mockEC2API) DescribeVpcEndpoints(ctx context.Context, params *ec2.DescribeVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	if m.describeVpcEndpoints != nil {
		return m.describeVpcEndpoints(ctx, params, optFns...)
	}
	return &ec2.DescribeVpcEndpointsOutput{}, nil
}
```

**Step 3: Build**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build.

**Step 4: Commit**

```bash
git add pkg/aws/ec2iface.go pkg/aws/ec2iface_mock_test.go
git commit -m "$(cat <<'EOF'
feat: add DescribeVpcEndpoints to EC2API interface

Needed by the upcoming eks endpoint command for VPC endpoint checks.
EOF
)"
```

### Task 7: Add VPC endpoint DNS resolution and API check logic

**Files:**
- Create: `pkg/aws/endpoint.go`
- Create: `pkg/aws/endpoint_test.go`

**Step 1: Write failing tests**

Create `pkg/aws/endpoint_test.go`:
```go
package aws

import (
	"net"
	"testing"
)

func TestClassifyIP_Private(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"10.0.1.5", "private"},
		{"172.16.0.1", "private"},
		{"172.31.255.255", "private"},
		{"192.168.1.1", "private"},
		{"54.239.28.85", "public"},
		{"3.5.140.2", "public"},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := classifyIP(net.ParseIP(tt.ip))
			if got != tt.want {
				t.Errorf("classifyIP(%s) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestBuildServiceEndpoints(t *testing.T) {
	endpoints := buildServiceEndpoints("us-east-1")
	if len(endpoints) == 0 {
		t.Fatal("expected non-empty service endpoints")
	}

	// Verify expected services are present.
	found := map[string]bool{}
	for _, ep := range endpoints {
		found[ep.ServiceKey] = true
	}
	for _, key := range []string{"sts", "ec2", "ecr.api", "ecr.dkr", "s3", "logs"} {
		if !found[key] {
			t.Errorf("missing expected service key %q", key)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./pkg/aws/ -run "TestClassifyIP|TestBuildServiceEndpoints" -v`
Expected: FAIL — undefined functions.

**Step 3: Write the implementation**

Create `pkg/aws/endpoint.go`:
```go
package aws

import (
	"context"
	"fmt"
	"net"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ServiceEndpoint represents an AWS service to check for VPC endpoints.
type ServiceEndpoint struct {
	ServiceKey  string // short name: "sts", "ec2", "ecr.api", etc.
	Hostname    string // DNS hostname to resolve
	AWSService  string // full AWS service name for DescribeVpcEndpoints filter
}

// EndpointCheckResult holds the result of checking a single service.
type EndpointCheckResult struct {
	ServiceKey   string `json:"service_key"`
	DNSResult    string `json:"dns_result"`    // "private", "public", "unresolved"
	ResolvedIPs  []string `json:"resolved_ips,omitempty"`
	EndpointType string `json:"endpoint_type,omitempty"` // "Interface", "Gateway", ""
	EndpointID   string `json:"endpoint_id,omitempty"`
	State        string `json:"state,omitempty"`
}

// buildServiceEndpoints returns the list of AWS services to check for VPC endpoints.
func buildServiceEndpoints(region string) []ServiceEndpoint {
	return []ServiceEndpoint{
		{ServiceKey: "sts", Hostname: fmt.Sprintf("sts.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.sts", region)},
		{ServiceKey: "ec2", Hostname: fmt.Sprintf("ec2.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ec2", region)},
		{ServiceKey: "ecr.api", Hostname: fmt.Sprintf("api.ecr.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ecr.api", region)},
		{ServiceKey: "ecr.dkr", Hostname: fmt.Sprintf("dkr.ecr.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.ecr.dkr", region)},
		{ServiceKey: "s3", Hostname: fmt.Sprintf("s3.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.s3", region)},
		{ServiceKey: "logs", Hostname: fmt.Sprintf("logs.%s.amazonaws.com", region), AWSService: fmt.Sprintf("com.amazonaws.%s.logs", region)},
	}
}

// classifyIP returns "private" for RFC 1918 addresses, "public" otherwise.
func classifyIP(ip net.IP) string {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{mustParseCIDR("10.0.0.0/8")},
		{mustParseCIDR("172.16.0.0/12")},
		{mustParseCIDR("192.168.0.0/16")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return "private"
		}
	}
	return "public"
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// DNSResolver is a function type for resolving hostnames. Allows test injection.
type DNSResolver func(host string) ([]net.IP, error)

// DefaultDNSResolver uses net.LookupIP for DNS resolution.
func DefaultDNSResolver(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// CheckEndpointDNS resolves the hostname and classifies the result.
func CheckEndpointDNS(ep ServiceEndpoint, resolver DNSResolver) EndpointCheckResult {
	result := EndpointCheckResult{ServiceKey: ep.ServiceKey}

	ips, err := resolver(ep.Hostname)
	if err != nil || len(ips) == 0 {
		result.DNSResult = "unresolved"
		return result
	}

	// Classify based on first IP (all should be same class for a VPC endpoint).
	result.DNSResult = classifyIP(ips[0])
	for _, ip := range ips {
		result.ResolvedIPs = append(result.ResolvedIPs, ip.String())
	}

	return result
}

// EnrichWithVpcEndpoints calls DescribeVpcEndpoints and enriches results with endpoint details.
func EnrichWithVpcEndpoints(ctx context.Context, api EC2API, region string, results []EndpointCheckResult) []EndpointCheckResult {
	endpoints := buildServiceEndpoints(region)

	// Build service name -> result index map.
	svcToIdx := map[string]int{}
	for i, r := range results {
		for _, ep := range endpoints {
			if ep.ServiceKey == r.ServiceKey {
				svcToIdx[ep.AWSService] = i
				break
			}
		}
	}

	// Collect all service names to query.
	svcNames := make([]string, 0, len(svcToIdx))
	for sn := range svcToIdx {
		svcNames = append(svcNames, sn)
	}

	if len(svcNames) == 0 {
		return results
	}

	out, err := api.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("service-name"), Values: svcNames},
		},
	})
	if err != nil {
		// Degrade gracefully — return DNS-only results.
		return results
	}

	for _, vpce := range out.VpcEndpoints {
		svcName := aws.ToString(vpce.ServiceName)
		idx, ok := svcToIdx[svcName]
		if !ok {
			continue
		}
		results[idx].EndpointID = aws.ToString(vpce.VpcEndpointId)
		results[idx].EndpointType = string(vpce.VpcEndpointType)
		results[idx].State = string(vpce.State)
	}

	return results
}
```

**Step 4: Run tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./pkg/aws/ -run "TestClassifyIP|TestBuildServiceEndpoints" -v`
Expected: All PASS.

**Step 5: Commit**

```bash
git add pkg/aws/endpoint.go pkg/aws/endpoint_test.go
git commit -m "$(cat <<'EOF'
feat: add VPC endpoint DNS resolution and API check logic

Resolves AWS service endpoints via DNS to classify as private/public.
Enriches results with DescribeVpcEndpoints when permissions allow.
EOF
)"
```

### Task 8: Add `kdiag eks endpoint` command

**Files:**
- Create: `cmd/eks/endpoint.go`

**Step 1: Write the command**

Create `cmd/eks/endpoint.go`:
```go
package eks

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	k8spkg "github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// EndpointReport is the top-level structured result for --output json.
type EndpointReport struct {
	Region      string                      `json:"region"`
	EKSPrivate  string                      `json:"eks_api_access"` // "private", "public", "unknown"
	Services    []awspkg.EndpointCheckResult `json:"services"`
	APIEnriched bool                        `json:"api_enriched"`
}

var endpointCmd = &cobra.Command{
	Use:   "endpoint",
	Short: "Check VPC endpoints for AWS services (STS, EC2, ECR, S3, CloudWatch Logs, EKS API)",
	RunE:  runEndpoint,
}

func init() {
	EksCmd.AddCommand(endpointCmd)
}

func runEndpoint(cmd *cobra.Command, args []string) error {
	client, err := k8spkg.NewClient(configFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	host := client.Config.Host
	if err := requireEKS(host); err != nil {
		return err
	}

	region := resolveRegion(host)
	if region == "" {
		return fmt.Errorf("could not determine AWS region; use --region to specify")
	}

	ctx, cancel := context.WithTimeout(context.Background(), getTimeout())
	defer cancel()

	endpoints := awspkg.BuildServiceEndpoints(region)

	// Phase 1: DNS resolution.
	results := make([]awspkg.EndpointCheckResult, 0, len(endpoints)+1)
	for _, ep := range endpoints {
		if isVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] resolving %s\n", ep.Hostname)
		}
		r := awspkg.CheckEndpointDNS(ep, awspkg.DefaultDNSResolver)
		results = append(results, r)
	}

	// EKS API private access check via DNS.
	eksResult := awspkg.CheckEndpointDNS(awspkg.ServiceEndpoint{
		ServiceKey: "eks-api",
		Hostname:   extractHostname(host),
	}, awspkg.DefaultDNSResolver)
	eksPrivate := eksResult.DNSResult

	// Phase 2: DescribeVpcEndpoints (optional).
	apiEnriched := false
	ec2Client, ec2Err := newEC2Client(ctx, host)
	if ec2Err == nil {
		results = awspkg.EnrichWithVpcEndpoints(ctx, ec2Client, region, results)
		apiEnriched = true
		if isVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] enriched results with DescribeVpcEndpoints\n")
		}
	} else if isVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] skipping DescribeVpcEndpoints: %v\n", ec2Err)
	}

	report := EndpointReport{
		Region:      region,
		EKSPrivate:  eksPrivate,
		Services:    results,
		APIEnriched: apiEnriched,
	}

	// Output.
	printer, err := output.NewPrinter(getOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(report)
	}

	return printEndpointTable(report)
}

func printEndpointTable(report EndpointReport) error {
	fmt.Fprintf(os.Stdout, "Region: %s\n", report.Region)
	fmt.Fprintf(os.Stdout, "EKS API Access: %s\n", report.EKSPrivate)
	if !report.APIEnriched {
		fmt.Fprintln(os.Stdout, "(DescribeVpcEndpoints unavailable — showing DNS results only)")
	}
	fmt.Fprintln(os.Stdout)

	p := output.NewTablePrinter(os.Stdout)
	if report.APIEnriched {
		p.PrintHeader("SERVICE", "DNS_RESULT", "ENDPOINT_TYPE", "ENDPOINT_ID", "STATE")
		for _, s := range report.Services {
			epType := s.EndpointType
			if epType == "" {
				epType = "-"
			}
			epID := s.EndpointID
			if epID == "" {
				epID = "-"
			}
			state := s.State
			if state == "" {
				state = "-"
			}
			p.PrintRow(s.ServiceKey, s.DNSResult, epType, epID, state)
		}
	} else {
		p.PrintHeader("SERVICE", "DNS_RESULT")
		for _, s := range report.Services {
			p.PrintRow(s.ServiceKey, s.DNSResult)
		}
	}

	return p.Flush()
}

// extractHostname strips scheme and port from a URL, returning just the hostname.
func extractHostname(host string) string {
	// host may be "https://ABCDEF.gr7.us-east-1.eks.amazonaws.com"
	// net/url.Parse needs a scheme to parse correctly.
	if len(host) > 0 && host[0] != 'h' {
		host = "https://" + host
	}
	// Use simple string manipulation to avoid import cycle.
	// Strip scheme.
	for _, prefix := range []string{"https://", "http://"} {
		if len(host) > len(prefix) && host[:len(prefix)] == prefix {
			host = host[len(prefix):]
			break
		}
	}
	// Strip port.
	if idx := len(host) - 1; idx > 0 {
		for i := len(host) - 1; i >= 0; i-- {
			if host[i] == ':' {
				host = host[:i]
				break
			}
			if host[i] == '/' || host[i] == ']' {
				break
			}
		}
	}
	// Strip trailing path.
	for i := 0; i < len(host); i++ {
		if host[i] == '/' {
			host = host[:i]
			break
		}
	}
	return host
}
```

**Step 2: Export BuildServiceEndpoints**

In `pkg/aws/endpoint.go`, rename `buildServiceEndpoints` to `BuildServiceEndpoints` (capitalize the B) so it's exported. Update the call in `EnrichWithVpcEndpoints` to use `BuildServiceEndpoints` as well.

**Step 3: Build**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./...`
Expected: Clean build.

**Step 4: Run all tests**

Run: `cd /Users/lgbarn/Personal/kdiag && go test ./...`
Expected: All tests pass.

**Step 5: Commit**

```bash
git add cmd/eks/endpoint.go pkg/aws/endpoint.go
git commit -m "$(cat <<'EOF'
feat: add kdiag eks endpoint command

Checks VPC endpoints for STS, EC2, ECR, S3, CloudWatch Logs via DNS
resolution (private vs public IP). Enriches with DescribeVpcEndpoints
when IAM permissions allow. Also checks EKS API private access.
EOF
)"
```

---

## Phase 5: Skill Updates

### Task 9: Update skill/SKILL.md

**Files:**
- Modify: `skill/SKILL.md`

**Step 1: Update flag references**

Replace `--aws-profile` with `--profile` and `--aws-region` with `--region` on line 129.

**Step 2: Add profile prompting guidance**

After the "First Steps" section (after line 28), add:
```
4. **Which AWS profile?** (for EKS commands — ask if not specified, pass `--profile <name>`)
```

**Step 3: Add new commands to quick reference table**

After the `eks node` entries (after line 65), add:
```
| `kdiag ingress <name>` | Inspect Ingress rules, backends, TLS, controller | `kdiag ingress my-ingress` |
| `kdiag eks endpoint` | Check VPC endpoints for AWS services | `kdiag eks endpoint` |
```

**Step 4: Add Ingress Issues playbook**

After the "EKS-Specific Issues" section, add:
```
### Ingress Issues

1. Run `kdiag ingress <name>` to inspect the Ingress resource
2. Check if backend Services exist and have ready endpoints
3. Check if TLS secrets exist
4. Check controller health (ALB or NGINX auto-detected)
5. If endpoints are missing, inspect the backend Service selector vs pod labels
6. If the controller is unhealthy, inspect controller pods with `kdiag inspect <controller-pod> -n kube-system`
```

**Step 5: Add VPC Endpoint playbook**

After the Ingress Issues section, add:
```
### VPC Endpoint / Private Connectivity

1. Run `kdiag eks endpoint` to check which AWS services route over the internet
2. Services showing "public" DNS result lack a VPC endpoint — traffic crosses the internet
3. Recommend creating VPC endpoints for any "public" services, especially:
   - **STS** — needed for IRSA token exchange
   - **ECR** (api + dkr) + **S3** — needed for image pulls
   - **CloudWatch Logs** — needed if using Fluent Bit / CloudWatch agent
4. If EKS API shows "public", recommend enabling private endpoint access in the EKS cluster config
```

**Step 6: Update Pod Not Running playbook**

In the "Pod Not Running" playbook, after the ImagePullBackOff bullet, add:
```
   - **CreateContainerConfigError**: `diagnose` checks ConfigMap/Secret refs — look for missing refs in the output
```

**Step 7: Update EKS-Specific Issues section**

Replace `--aws-profile` and `--aws-region` references with `--profile` and `--region`. Add `eks endpoint` to the numbered list.

**Step 8: Commit**

```bash
git add skill/SKILL.md
git commit -m "$(cat <<'EOF'
docs: update skill with new commands, --profile flag, and playbooks

Adds ingress, eks endpoint commands to quick reference. Adds Ingress
Issues and VPC Endpoint playbooks. Updates AWS flag names. Adds
profile prompting guidance.
EOF
)"
```

---

## Dependency Graph

```
Task 1 (flag rename) ──────────────────────────────────────┐
Task 2 (refs extraction) → Task 3 (wire into diagnose)     │
Task 4 (ingress cmd) → Task 5 (ingress in diagnose)        │
Task 6 (EC2API interface) → Task 7 (endpoint logic) → Task 8 (eks endpoint cmd)
                                                            │
Task 9 (skill updates) ← depends on all above ─────────────┘
```

**Parallelizable groups:**
- Tasks 1, 2, 4, 6 can all start in parallel (different files)
- Task 3 depends on Task 2
- Task 5 depends on Task 4
- Tasks 7, 8 depend on Task 6
- Task 9 depends on all others

**Verification after all tasks:**

Run: `cd /Users/lgbarn/Personal/kdiag && go build ./... && go test ./... && go vet ./...`
Expected: Clean build, all tests pass, no vet warnings.
