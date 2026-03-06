package k8s

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// Client wraps a Kubernetes clientset along with the REST config and resolved namespace.
type Client struct {
	Clientset kubernetes.Interface
	Config    *rest.Config
	Namespace string
}

// NewClient constructs a Client from cli-runtime ConfigFlags.
// It resolves the namespace from the --namespace flag, kubeconfig context,
// or "default" as a fallback. QPS and Burst are set higher than kubectl
// defaults to support diagnostic workloads.
func NewClient(configFlags *genericclioptions.ConfigFlags) (*Client, error) {
	restConfig, err := configFlags.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build REST config: %w", err)
	}

	restConfig.QPS = 50
	restConfig.Burst = 100

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	namespace, _, err := configFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve namespace: %w", err)
	}

	return &Client{
		Clientset: clientset,
		Config:    restConfig,
		Namespace: namespace,
	}, nil
}
