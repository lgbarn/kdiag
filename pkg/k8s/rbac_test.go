package k8s

import (
	"context"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	testing2 "k8s.io/client-go/testing"
)

// makeReactor returns a ReactionFunc that answers SelfSubjectAccessReview
// create calls by looking up the verb+subresource in the allowed map.
// Map key: "<verb>/<subresource>" → allowed bool.
func makeReactor(allowed map[string]bool) testing2.ReactionFunc {
	return func(action testing2.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(testing2.CreateAction)
		if !ok {
			return false, nil, nil
		}
		ssar, ok := createAction.GetObject().(*authorizationv1.SelfSubjectAccessReview)
		if !ok {
			return false, nil, nil
		}
		key := ssar.Spec.ResourceAttributes.Verb + "/" + ssar.Spec.ResourceAttributes.Subresource
		isAllowed := allowed[key]
		result := &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{
				Allowed: isAllowed,
			},
		}
		return true, result, nil
	}
}

func TestCheckEphemeralContainerRBAC_BothAllowed(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	fakeClient.PrependReactor("create", "selfsubjectaccessreviews", makeReactor(map[string]bool{
		"update/ephemeralcontainers": true,
		"create/attach":              true,
	}))

	checks, err := CheckEphemeralContainerRBAC(context.Background(), fakeClient, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
	for _, c := range checks {
		if !c.Allowed {
			t.Errorf("expected check %s/%s to be allowed, got denied", c.Verb, c.Resource)
		}
	}
}

func TestCheckEphemeralContainerRBAC_EphemeralContainersDenied(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	fakeClient.PrependReactor("create", "selfsubjectaccessreviews", makeReactor(map[string]bool{
		"update/ephemeralcontainers": false,
		"create/attach":              true,
	}))

	checks, err := CheckEphemeralContainerRBAC(context.Background(), fakeClient, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ephemeralCheck *RBACCheck
	for i := range checks {
		if checks[i].Verb == "update" && checks[i].Subresource == "ephemeralcontainers" {
			ephemeralCheck = &checks[i]
		}
	}
	if ephemeralCheck == nil {
		t.Fatal("expected an ephemeralcontainers check in the result")
	}
	if ephemeralCheck.Allowed {
		t.Error("expected ephemeralcontainers check to be denied")
	}
}

func TestFormatRBACError_IncludesDeniedAndRemediation(t *testing.T) {
	checks := []RBACCheck{
		{Verb: "update", Resource: "pods", Subresource: "ephemeralcontainers", Allowed: false},
		{Verb: "create", Resource: "pods", Subresource: "attach", Allowed: true},
	}

	msg := FormatRBACError(checks)

	if !strings.Contains(msg, "update") {
		t.Error("expected FormatRBACError to mention denied verb 'update'")
	}
	if !strings.Contains(msg, "ephemeralcontainers") {
		t.Error("expected FormatRBACError to mention denied subresource 'ephemeralcontainers'")
	}
	if !strings.Contains(msg, "remediation") || !strings.Contains(msg, "cluster admin") {
		t.Error("expected FormatRBACError to include remediation message referencing cluster admin")
	}
}
