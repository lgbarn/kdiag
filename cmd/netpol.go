package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lgbarn/kdiag/pkg/k8s"
	"github.com/lgbarn/kdiag/pkg/netpol"
	"github.com/lgbarn/kdiag/pkg/output"
)

var netpolCmd = &cobra.Command{
	Use:   "netpol <pod-name>",
	Short: "List NetworkPolicies that apply to a pod and summarize their rules",
	Args:  cobra.ExactArgs(1),
	RunE:  runNetpol,
}

func init() {
	rootCmd.AddCommand(netpolCmd)
}

func runNetpol(cmd *cobra.Command, args []string) error {
	podName := args[0]

	client, err := k8s.NewClient(ConfigFlags)
	if err != nil {
		return fmt.Errorf("error connecting to cluster: %w", err)
	}

	namespace := client.Namespace

	ctx, cancel := context.WithTimeout(context.Background(), GetTimeout())
	defer cancel()

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] resolving pod %q in namespace %q\n", podName, namespace)
	}

	// Resolve target pod.
	pod, err := client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("pod %q not found in namespace %q", podName, namespace)
		}
		return fmt.Errorf("failed to get pod %q: %w", podName, err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] listing NetworkPolicies in namespace %q\n", namespace)
	}

	// List all NetworkPolicies in namespace.
	policyList, err := client.Clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list NetworkPolicies in namespace %q: %w", namespace, err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "[kdiag] found %d NetworkPolicies; matching against pod labels\n", len(policyList.Items))
	}

	// Match policies against pod labels.
	matched, err := netpol.MatchingPolicies(policyList.Items, pod.Labels)
	if err != nil {
		return fmt.Errorf("matching NetworkPolicies: %w", err)
	}

	// Summarize matched policies.
	summaries := make([]netpol.PolicySummary, 0, len(matched))
	for _, policy := range matched {
		summaries = append(summaries, netpol.SummarizePolicy(policy))
	}

	// Build result.
	result := netpol.NetpolResult{
		Pod:      podName,
		Policies: summaries,
	}

	// Handle no matches.
	if len(summaries) == 0 {
		fmt.Fprintf(os.Stderr, "No NetworkPolicies select pod %q in namespace %q.\nThis means the pod's traffic is not restricted by NetworkPolicies.\n", podName, namespace)
	}

	// Output.
	printer, err := output.NewPrinter(GetOutputFormat(), os.Stdout)
	if err != nil {
		return fmt.Errorf("unsupported output format: %w", err)
	}

	if jp, ok := printer.(*output.JSONPrinter); ok {
		return jp.Print(result)
	}

	// Table output: structured text for nested policy display.
	for _, summary := range summaries {
		fmt.Fprintf(os.Stdout, "\nPolicy: %s\n", summary.Name)
		fmt.Fprintf(os.Stdout, "  Pod Selector: %s\n", summary.PodSelector)
		fmt.Fprintf(os.Stdout, "  Policy Types: %s\n", strings.Join(summary.PolicyTypes, ", "))

		if len(summary.Ingress) > 0 {
			fmt.Fprintf(os.Stdout, "  Ingress Rules:\n")
			for i, rule := range summary.Ingress {
				fmt.Fprintf(os.Stdout, "    Rule %d:\n", i+1)
				fmt.Fprintf(os.Stdout, "      Ports: %s\n", strings.Join(rule.Ports, ", "))
				if len(rule.From) > 0 {
					fmt.Fprintf(os.Stdout, "      From: %s\n", strings.Join(rule.From, ", "))
				}
				if len(rule.IPBlocks) > 0 {
					fmt.Fprintf(os.Stdout, "      IPBlocks: %s\n", strings.Join(rule.IPBlocks, ", "))
				}
			}
		}

		if len(summary.Egress) > 0 {
			fmt.Fprintf(os.Stdout, "  Egress Rules:\n")
			for i, rule := range summary.Egress {
				fmt.Fprintf(os.Stdout, "    Rule %d:\n", i+1)
				fmt.Fprintf(os.Stdout, "      Ports: %s\n", strings.Join(rule.Ports, ", "))
				if len(rule.To) > 0 {
					fmt.Fprintf(os.Stdout, "      To: %s\n", strings.Join(rule.To, ", "))
				}
				if len(rule.IPBlocks) > 0 {
					fmt.Fprintf(os.Stdout, "      IPBlocks: %s\n", strings.Join(rule.IPBlocks, ", "))
				}
			}
		}
	}

	return printer.Flush()
}
