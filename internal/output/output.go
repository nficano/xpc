// Package output provides uniform formatters used across the xpc subcommand
// surface. Every command that wants to honor `--output {text|json|table}`
// should funnel its results through one of the helpers here so the user
// sees consistent shapes across the CLI.
//
// Three primary entrypoints:
//
//	Encode(w, mode, v)              — single value (any JSON-serializable
//	                                  thing); table fallback uses Stringer
//	                                  or fmt.Sprint.
//	EncodeRows(w, mode, headers, rows)
//	                                — tabular data; table mode renders an
//	                                  aligned text table, json mode emits
//	                                  []map[header]cell, text mode prints
//	                                  space-separated columns.
//	EncodeKV(w, mode, pairs)        — flat key/value summary (e.g. agent.info);
//	                                  table mode aligns "key: value" pairs.
//
// Mode strings are case-insensitive; "" is treated as "text".
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Mode identifies an output mode. Use the package constants when comparing.
type Mode string

// Output modes accepted by the CLI's --output flag.
const (
	ModeText  Mode = "text"
	ModeJSON  Mode = "json"
	ModeTable Mode = "table"
)

// ParseMode normalizes a free-form mode string. Unknown inputs return ModeText.
// Empty input is treated as text.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return ModeJSON
	case "table":
		return ModeTable
	default:
		return ModeText
	}
}

// Encode emits a single value in the requested mode.
//
// json   -> indented JSON (always 2-space indent).
// table  -> falls through to text (single values aren't tabular).
// text   -> if v has a String() method it's used; otherwise fmt.Sprintln(v).
func Encode(w io.Writer, mode Mode, v any) error {
	if mode == ModeJSON {
		return writeJSON(w, v)
	}
	if s, ok := v.(fmt.Stringer); ok {
		_, err := fmt.Fprintln(w, s.String())
		return err
	}
	_, err := fmt.Fprintln(w, v)
	return err
}

// EncodeRows emits a tabular result.
//
//	headers: column names; len(headers) determines column count.
//	rows:    row[i] must be the same length as headers; values are converted
//	         with fmt.Sprint.
//
// In json mode, output is `[{header1: cell1, header2: cell2}, ...]`. In
// table mode, columns are tab-aligned. In text mode, columns are
// space-separated using a simple format.
func EncodeRows(w io.Writer, mode Mode, headers []string, rows [][]any) error {
	if mode == ModeJSON {
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			m := make(map[string]any, len(headers))
			for i, h := range headers {
				if i < len(r) {
					m[h] = r[i]
				} else {
					m[h] = nil
				}
			}
			out = append(out, m)
		}
		return writeJSON(w, out)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if mode == ModeTable && len(headers) > 0 {
		fmt.Fprintln(tw, strings.Join(headers, "\t"))
		sep := make([]string, len(headers))
		for i, h := range headers {
			sep[i] = strings.Repeat("-", len(h))
		}
		fmt.Fprintln(tw, strings.Join(sep, "\t"))
	}
	for _, r := range rows {
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(r) {
				cells[i] = fmt.Sprint(r[i])
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// KV is a key/value pair used by EncodeKV. The slice form (rather than a
// map) preserves caller-defined ordering, which matters for human-readable
// output.
type KV struct {
	Key   string
	Value any
}

// EncodeKV emits a flat key/value summary.
//
// json   -> {key1: val1, key2: val2}
// table  -> aligned "key  value" rows
// text   -> "key: value\n" rows
func EncodeKV(w io.Writer, mode Mode, pairs []KV) error {
	if mode == ModeJSON {
		m := make(map[string]any, len(pairs))
		for _, p := range pairs {
			m[p.Key] = p.Value
		}
		return writeJSON(w, m)
	}
	if mode == ModeTable {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, p := range pairs {
			fmt.Fprintf(tw, "%s\t%v\n", p.Key, p.Value)
		}
		return tw.Flush()
	}
	for _, p := range pairs {
		fmt.Fprintf(w, "%s: %v\n", p.Key, p.Value)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
