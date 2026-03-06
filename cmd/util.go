package cmd

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// derefReplicas safely dereferences a *int32 replica count, returning 1 if nil.
func derefReplicas(p *int32) int32 {
	if p != nil {
		return *p
	}
	return 1
}

// eventAge computes a human-readable age string from an event's timestamps.
func eventAge(lastTimestamp metav1.Time, eventTime metav1.MicroTime) string {
	if !lastTimestamp.IsZero() {
		return time.Since(lastTimestamp.Time).Truncate(time.Second).String()
	}
	if !eventTime.IsZero() {
		return time.Since(eventTime.Time).Truncate(time.Second).String()
	}
	return "unknown"
}

// boolStr converts a bool to "true"/"false" string for table output.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
