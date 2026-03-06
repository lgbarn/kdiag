package k8s

import (
	"context"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RBACCheck holds the result of a single SelfSubjectAccessReview check.
type RBACCheck struct {
	Verb        string
	Resource    string
	Subresource string
	Allowed     bool
}

// CheckEphemeralContainerRBAC performs two SelfSubjectAccessReview calls to
// verify that the current user has the permissions required to inject ephemeral
// containers and attach to them in the given namespace.
func CheckEphemeralContainerRBAC(ctx context.Context, client kubernetes.Interface, namespace string) ([]RBACCheck, error) {
	type request struct {
		verb        string
		resource    string
		subresource string
	}

	requests := []request{
		{verb: "update", resource: "pods", subresource: "ephemeralcontainers"},
		{verb: "create", resource: "pods", subresource: "attach"},
	}

	results := make([]RBACCheck, 0, len(requests))

	for _, req := range requests {
		ssar := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:   namespace,
					Verb:        req.verb,
					Resource:    req.resource,
					Subresource: req.subresource,
				},
			},
		}

		result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to check RBAC for %s %s/%s: %w", req.verb, req.resource, req.subresource, err)
		}

		results = append(results, RBACCheck{
			Verb:        req.verb,
			Resource:    req.resource,
			Subresource: req.subresource,
			Allowed:     result.Status.Allowed,
		})
	}

	return results, nil
}

// CheckSingleRBAC performs a single SelfSubjectAccessReview for the given verb,
// resource, and subresource in the specified namespace. It returns true if the
// action is allowed, or an error if the API call fails.
func CheckSingleRBAC(ctx context.Context, client kubernetes.Interface, namespace, verb, resource, subresource string) (bool, error) {
	ssar := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Resource:    resource,
				Subresource: subresource,
			},
		},
	}

	result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to check RBAC for %s %s: %w", verb, resource, err)
	}

	return result.Status.Allowed, nil
}

// FormatRBACError produces a human-readable error message listing denied
// permissions and a remediation message for the cluster admin.
func FormatRBACError(checks []RBACCheck) string {
	var denied []string
	for _, c := range checks {
		if !c.Allowed {
			denied = append(denied, fmt.Sprintf("  - %s %s/%s", c.Verb, c.Resource, c.Subresource))
		}
	}

	if len(denied) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Missing permissions:\n")
	for _, d := range denied {
		b.WriteString(d)
		b.WriteString("\n")
	}
	b.WriteString("\nremediation: to grant ephemeral container permissions, ask your cluster admin to add these rules:\n")
	b.WriteString("  - verbs: [\"update\"], resources: [\"pods/ephemeralcontainers\"]\n")
	b.WriteString("  - verbs: [\"create\"], resources: [\"pods/attach\"]\n")

	return b.String()
}
