package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	"github.com/lgbarn/kdiag/pkg/dns"
	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/netpol"
	"github.com/lgbarn/kdiag/pkg/output"
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose <pod>",
	Short: "Run all diagnostic checks against a pod and report pass/warn/fail status",
	Args:  cobra.ExactArgs(1),
	RunE:  runDiagnose,
}

func init() {
	rootCmd.AddCommand(diagnoseCmd)
}

func runDiagnose(cmd *cobra.Command, args []string) error {
	podName := args[0]

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
			Name: "inspect", Severity: "error",
			Summary: "pod inspection failed", Error: err.Error(),
		})
	} else {
		sev, sum := inspectSeverity(inspectResult)
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "inspect", Severity: sev, Summary: sum,
		})
	}

	// DNS check: CoreDNS pod health.
	coreDNSList, err := client.Clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=kube-dns",
	})
	if err != nil {
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "dns", Severity: "error",
			Summary: "failed to list CoreDNS pods", Error: err.Error(),
		})
	} else {
		pods := dns.EvaluateCoreDNSPods(coreDNSList.Items)
		sev, sum := corednsSeverity(pods)
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "dns", Severity: sev, Summary: sum,
		})
	}

	// Netpol check.
	pod, podErr := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if podErr != nil {
		report.Checks = append(report.Checks, DiagnoseCheckResult{
			Name: "netpol", Severity: "error",
			Summary: "failed to get pod for network policy check", Error: podErr.Error(),
		})
	} else {
		policies, listErr := client.Clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
		if listErr != nil {
			report.Checks = append(report.Checks, DiagnoseCheckResult{
				Name: "netpol", Severity: "error",
				Summary: "failed to list NetworkPolicies", Error: listErr.Error(),
			})
		} else {
			matched, matchErr := netpol.MatchingPolicies(policies.Items, pod.Labels)
			if matchErr != nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "netpol", Severity: "error",
					Summary: "failed to evaluate NetworkPolicies", Error: matchErr.Error(),
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

		ec2Client, ec2Err := awspkg.NewEC2Client(ctx, region, "")
		if ec2Err != nil {
			report.Checks = append(report.Checks,
				DiagnoseCheckResult{Name: "cni", Severity: "error", Summary: "failed to create EC2 client", Error: ec2Err.Error()},
				DiagnoseCheckResult{Name: "sg", Severity: "error", Summary: "failed to create EC2 client", Error: ec2Err.Error()},
			)
		} else {
			// EKS CNI check.
			ds, dsErr := client.Clientset.AppsV1().DaemonSets("kube-system").Get(ctx, "aws-node", metav1.GetOptions{})
			if dsErr != nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "cni", Severity: "error",
					Summary: "failed to get aws-node DaemonSet", Error: dsErr.Error(),
				})
			} else {
				dsHealthy := ds.Status.NumberReady == ds.Status.DesiredNumberScheduled
				nodeList, nodeErr := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if nodeErr != nil {
					report.Checks = append(report.Checks, DiagnoseCheckResult{
						Name: "cni", Severity: "error",
						Summary: "failed to list nodes", Error: nodeErr.Error(),
					})
				} else {
					exhaustedCount := countExhaustedNodes(ctx, ec2Client, nodeList.Items)
					sev, sum := cniSeverity(dsHealthy, exhaustedCount)
					report.Checks = append(report.Checks, DiagnoseCheckResult{
						Name: "cni", Severity: sev, Summary: sum,
					})
				}
			}

			// EKS SG check.
			sgPod, sgPodErr := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if sgPodErr != nil {
				report.Checks = append(report.Checks, DiagnoseCheckResult{
					Name: "sg", Severity: "error",
					Summary: "failed to get pod for SG check", Error: sgPodErr.Error(),
				})
			} else {
				sgIDs, sgErr := resolveENISGs(ctx, client, ec2Client, sgPod)
				if sgErr != nil {
					report.Checks = append(report.Checks, DiagnoseCheckResult{
						Name: "sg", Severity: "error",
						Summary: "failed to determine security groups", Error: sgErr.Error(),
					})
				} else {
					sgs, detailErr := awspkg.GetSecurityGroupDetails(ctx, ec2Client, sgIDs)
					if detailErr != nil {
						report.Checks = append(report.Checks, DiagnoseCheckResult{
							Name: "sg", Severity: "error",
							Summary: "failed to get security group details", Error: detailErr.Error(),
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
			DiagnoseCheckResult{Name: "cni", Severity: "skipped", Summary: "not an EKS cluster"},
			DiagnoseCheckResult{Name: "sg", Severity: "skipped", Summary: "not an EKS cluster"},
		)
	}

	report.Summary = computeSummary(report.Checks)

	if GetOutputFormat() == "json" {
		jp, err := output.NewJSONPrinter(os.Stdout)
		if err != nil {
			return err
		}
		return jp.Print(report)
	}

	if err := printDiagnoseTable(report); err != nil {
		return err
	}

	if report.Summary.Fail > 0 {
		return ErrDiagnoseFail
	}
	return nil
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
		case "pass":
			sevStr = color.GreenString("pass")
		case "warn":
			sevStr = color.YellowString("warn")
		case "fail", "error":
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
func countExhaustedNodes(ctx context.Context, ec2Client awspkg.EC2API, nodes []corev1.Node) int {
	type eligibleNode struct {
		name         string
		instanceType string
		instanceID   string
	}

	var eligible []eligibleNode
	uniqueTypes := map[string]struct{}{}

	for i := range nodes {
		node := &nodes[i]
		if k8s.IsFargateNode(node.Name) {
			continue
		}
		instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]
		if !ok || instanceType == "" {
			continue
		}
		instanceID, err := awspkg.ParseInstanceID(node.Spec.ProviderID)
		if err != nil {
			continue
		}
		eligible = append(eligible, eligibleNode{
			name: node.Name, instanceType: instanceType, instanceID: instanceID,
		})
		uniqueTypes[instanceType] = struct{}{}
	}

	typeList := make([]string, 0, len(uniqueTypes))
	for t := range uniqueTypes {
		typeList = append(typeList, t)
	}

	limitsMap, err := awspkg.GetInstanceTypeLimits(ctx, ec2Client, typeList)
	if err != nil {
		return 0
	}

	exhausted := 0
	for _, en := range eligible {
		eniInfo, err := awspkg.ListNodeENIs(ctx, ec2Client, en.instanceID)
		if err != nil {
			continue
		}
		limits := limitsMap[en.instanceType]
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
	return exhausted
}

// resolveENISGs returns the security group IDs for a pod: branch ENI SGs if
// the pod has the vpc.amazonaws.com/pod-eni annotation, or node primary ENI
// SGs otherwise.
func resolveENISGs(ctx context.Context, client *k8s.Client, ec2Client awspkg.EC2API, pod *corev1.Pod) ([]string, error) {
	const podENIAnnotation = "vpc.amazonaws.com/pod-eni"

	if annotation, ok := pod.Annotations[podENIAnnotation]; ok {
		eniAnnotations, err := awspkg.ParsePodENIAnnotation(annotation)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pod ENI annotation: %w", err)
		}
		if len(eniAnnotations) == 0 {
			return nil, fmt.Errorf("pod %q has empty pod-eni annotation", pod.Name)
		}
		sgIDs, err := awspkg.GetENISecurityGroups(ctx, ec2Client, eniAnnotations[0].ENIID)
		if err != nil {
			return nil, fmt.Errorf("failed to get security groups for branch ENI: %w", err)
		}
		return sgIDs, nil
	}

	node, err := client.Clientset.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node %q: %w", pod.Spec.NodeName, err)
	}
	instanceID, err := awspkg.ParseInstanceID(node.Spec.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse providerID %q: %w", node.Spec.ProviderID, err)
	}
	sgIDs, err := awspkg.GetNodePrimaryENISecurityGroups(ctx, ec2Client, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get primary ENI security groups: %w", err)
	}
	return sgIDs, nil
}
