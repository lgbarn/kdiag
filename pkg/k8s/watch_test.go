package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func makeTestPodsClientset() *fake.Clientset {
	web1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-1",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
	}
	web2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-2",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
	}
	api1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-1",
			Namespace: "default",
			Labels:    map[string]string{"app": "api"},
		},
	}
	return fake.NewSimpleClientset(web1, web2, api1)
}

func TestListPodsBySelector(t *testing.T) {
	fakeClient := makeTestPodsClientset()
	client := &Client{Clientset: fakeClient, Namespace: "default"}
	ctx := context.Background()

	pods, err := ListPodsBySelector(ctx, client, "default", "app=web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Errorf("expected 2 pods with selector app=web, got %d", len(pods))
	}
	names := map[string]bool{}
	for _, p := range pods {
		names[p.Name] = true
	}
	if !names["web-1"] || !names["web-2"] {
		t.Errorf("expected pods web-1 and web-2, got %v", names)
	}

	pods, err = ListPodsBySelector(ctx, client, "default", "app=api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Errorf("expected 1 pod with selector app=api, got %d", len(pods))
	}
	if pods[0].Name != "api-1" {
		t.Errorf("expected pod api-1, got %s", pods[0].Name)
	}
}

func TestListPodsBySelector_Empty(t *testing.T) {
	fakeClient := makeTestPodsClientset()
	client := &Client{Clientset: fakeClient, Namespace: "default"}
	ctx := context.Background()

	pods, err := ListPodsBySelector(ctx, client, "default", "app=nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("expected 0 pods, got %d", len(pods))
	}
}

// allEvents holds the events used by tests so the reactor can filter them.
var allEvents = []*corev1.Event{
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-1",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "web-1",
			Namespace: "default",
		},
	},
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-2",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "web-1",
			Namespace: "default",
		},
	},
	{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "event-3",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Deployment",
			Name:      "myapp",
			Namespace: "default",
		},
	},
}

// makeTestEventsClientset returns a fake clientset with a reactor that implements
// field selector filtering for events (the default fake tracker ignores field selectors).
func makeTestEventsClientset() *fake.Clientset {
	objs := make([]runtime.Object, len(allEvents))
	for i, e := range allEvents {
		objs[i] = e
	}
	fakeClient := fake.NewSimpleClientset(objs...)

	// Prepend a reactor that honours field selectors on event List calls.
	fakeClient.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		listAction, ok := action.(k8stesting.ListAction)
		if !ok {
			return false, nil, nil
		}
		restriction := listAction.GetListRestrictions()
		fs := restriction.Fields.String()

		// Parse simple comma-separated key=value field selector.
		filters := map[string]string{}
		for _, part := range strings.Split(fs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				filters[kv[0]] = kv[1]
			}
		}

		var matched []corev1.Event
		for _, e := range allEvents {
			if name, ok := filters["involvedObject.name"]; ok && e.InvolvedObject.Name != name {
				continue
			}
			if ns, ok := filters["involvedObject.namespace"]; ok && e.InvolvedObject.Namespace != ns {
				continue
			}
			if kind, ok := filters["involvedObject.kind"]; ok && e.InvolvedObject.Kind != kind {
				continue
			}
			matched = append(matched, *e)
		}
		return true, &corev1.EventList{Items: matched}, nil
	})

	return fakeClient
}

func TestListEvents(t *testing.T) {
	fakeClient := makeTestEventsClientset()
	client := &Client{Clientset: fakeClient, Namespace: "default"}
	ctx := context.Background()

	// Filter by kind and name: should return event-1 and event-2
	events, err := ListEvents(ctx, client, "default", "Pod", "web-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for Pod/web-1, got %d", len(events))
	}

	// Filter by name only (empty kind): should return event-3
	events, err = ListEvents(ctx, client, "default", "", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event for myapp, got %d", len(events))
	}
	if events[0].Name != "event-3" {
		t.Errorf("expected event-3, got %s", events[0].Name)
	}
}

func TestListEvents_NoResults(t *testing.T) {
	fakeClient := makeTestEventsClientset()
	client := &Client{Clientset: fakeClient, Namespace: "default"}
	ctx := context.Background()

	events, err := ListEvents(ctx, client, "default", "Pod", "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}
