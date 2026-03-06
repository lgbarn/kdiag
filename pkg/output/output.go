// Package output provides formatting utilities for kdiag command output.
// It supports table (text/tabwriter) and JSON (encoding/json) formats.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Printer is the interface that all output formatters must implement.
type Printer interface {
	// PrintHeader writes a header row with the given column names.
	PrintHeader(columns ...string)
	// PrintRow writes a data row with the given column values.
	PrintRow(values ...string)
	// Flush finalizes any buffered output and writes it to the underlying writer.
	Flush() error
}

// TablePrinter formats output as a tab-aligned table using text/tabwriter.
type TablePrinter struct {
	w *tabwriter.Writer
}

// NewTablePrinter constructs a TablePrinter that writes to w.
func NewTablePrinter(w io.Writer) *TablePrinter {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	return &TablePrinter{w: tw}
}

// PrintHeader writes the header row to the tabwriter buffer.
func (t *TablePrinter) PrintHeader(columns ...string) {
	fmt.Fprintln(t.w, strings.Join(columns, "\t"))
}

// PrintRow writes a data row to the tabwriter buffer.
func (t *TablePrinter) PrintRow(values ...string) {
	fmt.Fprintln(t.w, strings.Join(values, "\t"))
}

// Flush flushes the tabwriter, producing aligned output.
func (t *TablePrinter) Flush() error {
	return t.w.Flush()
}

// JSONPrinter formats output as indented JSON using encoding/json.
type JSONPrinter struct {
	w io.Writer
}

// NewJSONPrinter constructs a JSONPrinter that writes to w.
func NewJSONPrinter(w io.Writer) (*JSONPrinter, error) {
	return &JSONPrinter{w: w}, nil
}

// PrintHeader is a no-op for JSONPrinter; JSON output has no tabular header.
func (j *JSONPrinter) PrintHeader(columns ...string) {}

// PrintRow is a no-op for JSONPrinter; use Print for structured JSON output.
func (j *JSONPrinter) PrintRow(values ...string) {}

// Flush is a no-op for JSONPrinter; JSON is written synchronously in Print.
func (j *JSONPrinter) Flush() error { return nil }

// Print marshals v as indented JSON and writes it to the underlying writer.
func (j *JSONPrinter) Print(v interface{}) error {
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NewPrinter is a factory that returns a Printer for the given format string.
// Supported formats: "table", "json".
// Returns an error if the format is not supported.
func NewPrinter(format string, w io.Writer) (Printer, error) {
	switch format {
	case "table":
		return NewTablePrinter(w), nil
	case "json":
		p, err := NewJSONPrinter(w)
		if err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unsupported output format %q: must be one of: table, json", format)
	}
}
