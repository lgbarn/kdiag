package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lgbarn/kdiag/pkg/output"
)

// TestTablePrinterProducesTabAlignedOutput verifies that TablePrinter writes
// tab-separated columns aligned by text/tabwriter.
func TestTablePrinterProducesTabAlignedOutput(t *testing.T) {
	var buf bytes.Buffer
	p, err := output.NewPrinter("table", &buf)
	if err != nil {
		t.Fatalf("NewPrinter(table) unexpected error: %v", err)
	}

	p.PrintHeader("NAME", "NAMESPACE", "STATUS")
	p.PrintRow("my-pod", "default", "Running")
	p.PrintRow("other-pod", "kube-system", "Pending")
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush() unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "NAME") {
		t.Errorf("expected output to contain NAME header, got:\n%s", out)
	}
	if !strings.Contains(out, "my-pod") {
		t.Errorf("expected output to contain my-pod, got:\n%s", out)
	}
	if !strings.Contains(out, "other-pod") {
		t.Errorf("expected output to contain other-pod, got:\n%s", out)
	}
	// Tab-aligned: columns must be separated by whitespace (tabwriter pads them)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines (header + 2 rows), got %d:\n%s", len(lines), out)
	}
	// Each line should have consistent column alignment (whitespace gaps between columns)
	for _, line := range lines {
		if !strings.Contains(line, " ") {
			t.Errorf("expected whitespace-padded columns in line %q", line)
		}
	}
}

// TestJSONPrinterProducesValidIndentedJSON verifies that JSONPrinter outputs
// valid, indented JSON for a given value.
func TestJSONPrinterProducesValidIndentedJSON(t *testing.T) {
	var buf bytes.Buffer
	jp, err := output.NewJSONPrinter(&buf)
	if err != nil {
		t.Fatalf("NewJSONPrinter unexpected error: %v", err)
	}

	type podInfo struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Status    string `json:"status"`
	}

	pod := podInfo{Name: "my-pod", Namespace: "default", Status: "Running"}
	if err := jp.Print(pod); err != nil {
		t.Fatalf("Print() unexpected error: %v", err)
	}

	out := buf.String()
	// Must be valid JSON
	var result podInfo
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput was:\n%s", err, out)
	}
	if result.Name != "my-pod" {
		t.Errorf("expected name=my-pod, got %s", result.Name)
	}
	// Must be indented (multi-line)
	if !strings.Contains(out, "\n") {
		t.Errorf("expected indented (multi-line) JSON, got single line: %s", out)
	}
	if !strings.Contains(out, "  ") {
		t.Errorf("expected indented JSON with spaces, got: %s", out)
	}
}

// TestNewPrinterReturnsTablePrinter verifies factory returns TablePrinter for "table".
func TestNewPrinterReturnsTablePrinter(t *testing.T) {
	var buf bytes.Buffer
	p, err := output.NewPrinter("table", &buf)
	if err != nil {
		t.Fatalf("NewPrinter(table) unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("NewPrinter(table) returned nil")
	}
}

// TestNewPrinterReturnsJSONPrinterForJSON verifies factory returns a Printer
// for the "json" format.
func TestNewPrinterReturnsJSONPrinterForJSON(t *testing.T) {
	var buf bytes.Buffer
	p, err := output.NewPrinter("json", &buf)
	if err != nil {
		t.Fatalf("NewPrinter(json) unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("NewPrinter(json) returned nil")
	}
}

// TestNewPrinterReturnsErrorForUnsupportedFormat verifies factory returns an
// error for unknown format strings.
func TestNewPrinterReturnsErrorForUnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	p, err := output.NewPrinter("yaml", &buf)
	if err == nil {
		t.Fatal("expected error for unsupported format 'yaml', got nil")
	}
	if p != nil {
		t.Errorf("expected nil printer for unsupported format, got %v", p)
	}
}
