package eks

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awspkg "github.com/lgbarn/kdiag/pkg/aws"
	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/output"
)

// SGReport holds the full security-group report for a pod.
type SGReport struct {
	PodName        string                    `json:"pod_name"`
	Namespace      string                    `json:"namespace"`
	NodeName       string                    `json:"node_name"`
	ENISource      string                    `json:"eni_source"`
	ENIID          string                    `json:"eni_id"`
	SecurityGroups []awspkg.SecurityGroupDetail `json:"security_groups"`
}

var sgCmd = &cobra.Command{
	Use:   "sg <pod>",
	Short: "Show effective security groups for a pod (branch ENI or node primary ENI)",
	Long: `Lookup the security groups that apply to a pod's network traffic.

For pods using Security Groups for Pods (vpc.amazonaws.com/pod-eni annotation),
the branch ENI's security groups are shown. For all other pods, the node's
primary ENI security groups are shown (inherited from the node).`,
	Args: cobra.ExactArgs(1),
	RunE: runSG,
}

func init() {
	EksCmd.AddCommand(sgCmd)
}

func runSG(cmd *cobra.Command, args []string) error {
	podName := args[0]

	// Build Kubernetes client.
	client, err := k8s.NewClient(configFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	// Verify the cluster is EKS.
	host := client.Config.Host
	if err := requireEKS(host); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), getTimeout())
	defer cancel()

	namespace := client.Namespace

	// Fetch the pod.
	pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod %q in namespace %q: %w", podName, namespace, err)
	}

	// Fargate check: use label-aware detection to identify Fargate pods.
	if k8s.DetectComputeType(pod) == k8s.ComputeTypeFargate {
		return fmt.Errorf("pod %q appears to be a Fargate pod; "+
			"Fargate pods manage networking outside EC2 ENIs and are not supported by this command", podName)
	}

	// Build EC2 client.
	ec2Client, err := newEC2Client(ctx, host)
	if err != nil {
		return fmt.Errorf("failed to create EC2 client: %w", err)
	}

	report := SGReport{
		PodName:   podName,
		Namespace: namespace,
		NodeName:  pod.Spec.NodeName,
	}

	// Determine ENI source: branch-ENI (security groups for pods) or node primary ENI.
	// Set metadata based on annotation presence before resolving SG IDs.
	if annotation, ok := pod.Annotations[awspkg.PodENIAnnotationKey]; ok {
		report.ENISource = "branch-eni (security groups for pods)"
		if parsed, parseErr := awspkg.ParsePodENIAnnotation(annotation); parseErr == nil && len(parsed) > 0 {
			report.ENIID = parsed[0].ENIID
		}
	} else {
		report.ENISource = "node-primary-eni (inherited from node)"
	}

	sgIDs, err := ResolveENISGs(ctx, client, ec2Client, pod)
	if err != nil {
		return fmt.Errorf("failed to resolve ENI security groups: %w", err)
	}

	// Fetch full security group details.
	report.SecurityGroups, err = awspkg.GetSecurityGroupDetails(ctx, ec2Client, sgIDs)
	if err != nil {
		return fmt.Errorf("failed to get security group details: %w", err)
	}

	// Output.
	printer, err := output.NewPrinter(getOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(report)
	}

	return printSGTable(report)
}

// formatPortRange returns display strings for FROM and TO port columns.
// All-traffic rules (protocol "all" or both ports zero) are shown as "*".
func formatPortRange(protocol string, from, to int32) (string, string) {
	if protocol == "all" || (from == 0 && to == 0) {
		return "*", "*"
	}
	return fmt.Sprintf("%d", from), fmt.Sprintf("%d", to)
}

// printSGTable renders the SGReport as structured human-readable table output.
func printSGTable(r SGReport) error {
	fmt.Fprintf(os.Stdout, "Pod:       %s/%s\n", r.Namespace, r.PodName)
	fmt.Fprintf(os.Stdout, "Node:      %s\n", r.NodeName)
	fmt.Fprintf(os.Stdout, "ENI Source: %s\n", r.ENISource)
	if r.ENIID != "" {
		fmt.Fprintf(os.Stdout, "ENI ID:    %s\n", r.ENIID)
	}
	fmt.Fprintln(os.Stdout)

	if len(r.SecurityGroups) == 0 {
		fmt.Fprintln(os.Stdout, "No security groups found.")
		return nil
	}

	for _, sg := range r.SecurityGroups {
		fmt.Fprintf(os.Stdout, "Security Group: %s (%s)\n", sg.GroupID, sg.GroupName)
		fmt.Fprintf(os.Stdout, "  Description: %s\n", sg.Description)

		// Ingress rules.
		fmt.Fprintln(os.Stdout, "  Ingress Rules:")
		if len(sg.IngressRules) == 0 {
			fmt.Fprintln(os.Stdout, "    (none)")
		} else {
			ingressPrinter, err := output.NewPrinter("table", os.Stdout)
			if err != nil {
				return err
			}
			ingressPrinter.PrintHeader("    PROTOCOL", "FROM", "TO", "SOURCE", "DESCRIPTION")
			for _, rule := range sg.IngressRules {
				source := strings.Join(rule.CIDRs, ",")
				if len(rule.SourceGroups) > 0 {
					if source != "" {
						source += ","
					}
					source += strings.Join(rule.SourceGroups, ",")
				}
				fromPort, toPort := formatPortRange(rule.Protocol, rule.FromPort, rule.ToPort)
				ingressPrinter.PrintRow(
					"    "+rule.Protocol,
					fromPort,
					toPort,
					source,
					rule.Description,
				)
			}
			if err := ingressPrinter.Flush(); err != nil {
				return err
			}
		}

		// Egress rules.
		fmt.Fprintln(os.Stdout, "  Egress Rules:")
		if len(sg.EgressRules) == 0 {
			fmt.Fprintln(os.Stdout, "    (none)")
		} else {
			egressPrinter, err := output.NewPrinter("table", os.Stdout)
			if err != nil {
				return err
			}
			egressPrinter.PrintHeader("    PROTOCOL", "FROM", "TO", "DESTINATION", "DESCRIPTION")
			for _, rule := range sg.EgressRules {
				dest := strings.Join(rule.CIDRs, ",")
				if len(rule.SourceGroups) > 0 {
					if dest != "" {
						dest += ","
					}
					dest += strings.Join(rule.SourceGroups, ",")
				}
				fromPort, toPort := formatPortRange(rule.Protocol, rule.FromPort, rule.ToPort)
				egressPrinter.PrintRow(
					"    "+rule.Protocol,
					fromPort,
					toPort,
					dest,
					rule.Description,
				)
			}
			if err := egressPrinter.Flush(); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

// ResolveENISGs returns the security group IDs for a pod: branch ENI SGs if
// the pod has the vpc.amazonaws.com/pod-eni annotation, or node primary ENI
// SGs otherwise.
func ResolveENISGs(ctx context.Context, client *k8s.Client, ec2Client awspkg.EC2API, pod *corev1.Pod) ([]string, error) {
	if annotation, ok := pod.Annotations[awspkg.PodENIAnnotationKey]; ok {
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
