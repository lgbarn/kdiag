package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eks "github.com/lgbarn/kdiag/cmd/eks"
	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	"github.com/lgbarn/kdiag/pkg/dns"
	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/netpol"
	"github.com/lgbarn/kdiag/pkg/output"
)

// urlPattern matches http(s) URLs that may appear in error messages.
var urlPattern = regexp.MustCompile(`https?://[^\s"']+`)

// sanitizeError strips URLs and truncates long error messages to avoid leaking
// infrastructure topology (cluster endpoints, AWS request IDs) in JSON output.
func sanitizeError(msg string) string {
	sanitized := urlPattern.ReplaceAllStringFunc(msg, func(rawURL string) string {
		u, err := url.Parse(rawURL)
		if err != nil {
			return "<redacted-url>"
		}
		return u.Scheme + "://" + u.Hostname() + "/..."
	})
	if len(sanitized) > 256 {
		sanitized = sanitized[:256] + "..."
	}
	return sanitized
}

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose <pod-name>",
	Short: "Run all diagnostic checks against a pod and report pass/warn/fail status",
	Args:  cobra.ExactArgs(1),
	RunE:  runDiagnose,
}

func init() {
	rootCmd.AddCommand(diagnoseCmd)
}

func runDiagnose(cmd *cobra.Command, args []string) error {
	podName := StripPodPrefix(args[0])

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}
	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	report := DiagnoseReport{Pod: podName, Namespace: namespace}
	report.IsEKS = awspkg.IsEKSCluster(client.Config.Host)

	// Inspect check.
	inspectResult, err := inspectPod(ctx, client, namespace, podName)
	if err != nil {
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "inspect", Severity: SeverityError,
			Summary: "pod inspection failed", Error: sanitizeError(err.Error()),
		})
	} else {
		sev, sum := inspectSeverity(inspectResult)
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "inspect", Severity: sev, Summary: sum,
		})
	}

	// Fetch the pod object once; reused by refs, netpol, and sg checks.
	pod, podErr := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})

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
				var getErr error
				if ref.Kind == "ConfigMap" {
					_, getErr = client.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
				} else {
					_, getErr = client.Clientset.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
				}
				if getErr != nil {
					if apierrors.IsNotFound(getErr) {
						if ref.Optional {
							optionalMissing = append(optionalMissing, ref)
						} else {
							missing = append(missing, ref)
						}
					}
				}
			}
			sev, sum := refsSeverity(missing, optionalMissing, len(refs))
			report.Checks = append(report.Checks, DiagnoseCheckResult{
				Name: "refs", Severity: sev, Summary: sum,
			})
		}
	}

	// DNS check: CoreDNS pod health.
	coreDNSList, err := client.Clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=kube-dns",
	})
	if err != nil {
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "dns", Severity: SeverityError,
			Summary: "failed to list CoreDNS pods", Error: sanitizeError(err.Error()),
		})
	} else {
		pods := dns.EvaluateCoreDNSPods(coreDNSList.Items)
		sev, sum := corednsSeverity(pods)
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "dns", Severity: sev, Summary: sum,
		})
	}

	// Netpol check.
	if podErr != nil {
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "netpol", Severity: SeverityError,
			Summary: "failed to get pod for network policy check", Error: sanitizeError(podErr.Error()),
		})
	} else {
		policies, listErr := client.Clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
		if listErr != nil {
			report.Checks = append(report.Checks, DiagnoseCheckResult{
				Name: "netpol", Severity: SeverityError,
				Summary: "failed to list NetworkPolicies", Error: sanitizeError(listErr.Error()),
			})
		} else {
			matched, matchErr := netpol.MatchingPolicies(policies.Items, pod.Labels)
			if matchErr != nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "netpol", Severity: SeverityError,
					Summary: "failed to evaluate NetworkPolicies", Error: sanitizeError(matchErr.Error()),
				})
			} else {
				summaries := make([]netpol.PolicySummary, 0, len(matched))
				for _, p := range matched {
					summaries = append(summaries, netpol.SummarizePolicy(p))
				}
				result := netpol.NetpolResult{Pod: podName, Policies: summaries}
				sev, sum := netpolSeverity(result)
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "netpol", Severity: sev, Summary: sum,
				})
			}
		}
	}

	// EKS-specific checks.
	if report.IsEKS {
		host := client.Config.Host
		region, regionErr := awspkg.RegionFromHost(host)
		if regionErr != nil && IsVerbose() {
			fmt.Fprintf(os.Stderr, "[kdiag] could not detect region from host %q: %v; falling back to AWS config\n", host, regionErr)
		}

		ec2Client, ec2Err := awspkg.NewEC2Client(ctx, region, eks.GetAWSProfile())
		if ec2Err != nil {
			report.Checks = append(report.Checks,
				DiagnoseCheckResult{Name: "cni", Severity: SeverityError, Summary: "failed to create EC2 client", Error: sanitizeError(ec2Err.Error())},
				DiagnoseCheckResult{Name: "sg", Severity: SeverityError, Summary: "failed to create EC2 client", Error: sanitizeError(ec2Err.Error())},
			)
		} else {
			// EKS CNI check.
			ds, dsErr := client.Clientset.AppsV1().DaemonSets("kube-system").Get(ctx, "aws-node", metav1.GetOptions{})
			if dsErr != nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "cni", Severity: SeverityError,
					Summary: "failed to get aws-node DaemonSet", Error: sanitizeError(dsErr.Error()),
				})
			} else {
				dsHealthy := ds.Status.NumberReady == ds.Status.DesiredNumberScheduled
				nodeList, nodeErr := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if nodeErr != nil {
					report.Checks = append(report.Checks, DiagnoseCheckResult{
						Name: "cni", Severity: SeverityError,
						Summary: "failed to list nodes", Error: sanitizeError(nodeErr.Error()),
					})
				} else {
					exhaustedCount, countErr := countExhaustedNodes(ctx, ec2Client, nodeList.Items)
					if countErr != nil {
						report.Checks = append(report.Checks, DiagnoseCheckResult{
							Name: "cni", Severity: SeverityError,
							Summary: "failed to count exhausted nodes", Error: sanitizeError(countErr.Error()),
						})
					} else {
						sev, sum := cniSeverity(dsHealthy, exhaustedCount)
						report.Checks = append(report.Checks, DiagnoseCheckResult{
							Name: "cni", Severity: sev, Summary: sum,
						})
					}
				}
			}

			// EKS SG check: reuse pod fetched above for the netpol check.
			if pod == nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "sg", Severity: SeverityError,
					Summary: "failed to get pod for SG check", Error: sanitizeError(podErr.Error()),
				})
			} else {
				sgIDs, sgErr := eks.ResolveENISGs(ctx, client, ec2Client, pod)
				if sgErr != nil {
					report.Checks = append(report.Checks, DiagnoseCheckResult{
						Name: "sg", Severity: SeverityError,
						Summary: "failed to determine security groups", Error: sanitizeError(sgErr.Error()),
					})
				} else {
					sgs, detailErr := awspkg.GetSecurityGroupDetails(ctx, ec2Client, sgIDs)
					if detailErr != nil {
						report.Checks = append(report.Checks, DiagnoseCheckResult{
							Name: "sg", Severity: SeverityError,
							Summary: "failed to get security group details", Error: sanitizeError(detailErr.Error()),
						})
					} else {
						sev, sum := sgSeverity(len(sgs))
						report.Checks = append(report.Checks, DiagnoseCheckResult{
							Name: "sg", Severity: sev, Summary: sum,
						})
					}
				}
			}
		}
	} else {
		report.Checks = append(report.Checks,
			DiagnoseCheckResult{Name: "cni", Severity: SeveritySkipped, Summary: "not an EKS cluster"},
			DiagnoseCheckResult{Name: "sg", Severity: SeveritySkipped, Summary: "not an EKS cluster"},
		)
	}

	report.Summary = computeSummary(report.Checks)

	var diagnoseErr error
	if report.Summary.Fail > 0 {
		diagnoseErr = ErrDiagnoseFail
	}

	if GetOutputFormat() == "json" {
		jp, err := output.NewJSONPrinter(os.Stdout)
		if err != nil {
			return err
		}
		if err := jp.Print(report); err != nil {
			return err
		}
		return diagnoseErr
	}

	if err := printDiagnoseTable(report); err != nil {
		return err
	}
	return diagnoseErr
}

func printDiagnoseTable(report DiagnoseReport) error {
	eksStatus := "no"
	if report.IsEKS {
		eksStatus = "yes"
	}
	fmt.Fprintf(os.Stdout, "Diagnosing pod: %s/%s\n", report.Namespace, report.Pod)
	fmt.Fprintf(os.Stdout, "EKS cluster: %s\n\n", eksStatus)

	p := output.NewTablePrinter(os.Stdout)
	p.PrintHeader("CHECK", "SEVERITY", "SUMMARY")
	for _, check := range report.Checks {
		var sevStr string
		switch check.Severity {
		case SeverityPass:
			sevStr = color.GreenString(SeverityPass)
		case SeverityWarn:
			sevStr = color.YellowString(SeverityWarn)
		case SeverityFail, SeverityError:
			sevStr = color.RedString(check.Severity)
		default:
			sevStr = check.Severity
		}

		summary := check.Summary
		if check.Error != "" {
			summary += ": " + check.Error
		}

		p.PrintRow(check.Name, sevStr, summary)
	}
	if err := p.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "\nSummary: %d total, %d pass, %d warn, %d fail, %d error, %d skipped\n",
		report.Summary.Total, report.Summary.Pass, report.Summary.Warn,
		report.Summary.Fail, report.Summary.Error, report.Summary.Skipped)

	return nil
}

// countExhaustedNodes iterates nodes, queries ENI usage per node, and returns
// the count of nodes with IP utilization at or above 85%.
func countExhaustedNodes(ctx context.Context, ec2Client awspkg.EC2API, nodes []corev1.Node) (int, error) {
	eligible, _ := eks.ClassifyNodes(nodes)

	uniqueTypes := map[string]struct{}{}
	for _, en := range eligible {
		uniqueTypes[en.InstanceType] = struct{}{}
	}

	typeList := make([]string, 0, len(uniqueTypes))
	for t := range uniqueTypes {
		typeList = append(typeList, t)
	}

	limitsMap, err := awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)
	if err != nil {
		return 0, fmt.Errorf("failed to get instance type limits: %w", err)
	}

	exhausted := 0
	for _, en := range eligible {
		eniInfo, err := awspkg.ListNodeENIs(ctx, ec2Client, en.InstanceID)
		if err != nil {
			continue
		}
		limits := limitsMap[en.InstanceType]
		if limits == nil {
			continue
		}
		maxTotalIPs := int(limits.MaxENIs) * int(limits.MaxIPsPerENI)
		if maxTotalIPs == 0 {
			continue
		}
		utilPct := (eniInfo.TotalIPs * 100) / maxTotalIPs
		if utilPct >= 85 {
			exhausted++
		}
	}
	return exhausted, nil
}

