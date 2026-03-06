package cmd

import "errors"

// ErrHealthCritical is returned by runHealth when the cluster has critical issues.
// main.go detects this sentinel to suppress the error message (issues are already
// printed in the health report table) while still setting exit code 1.
var ErrHealthCritical = errors.New("health: critical issues found")
