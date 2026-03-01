package gqlcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/toon-format/toon-go"
)

// JSONFormatter outputs data as JSON
type JSONFormatter struct {
	pretty bool
}

// NewJSONFormatter creates a JSON formatter
func NewJSONFormatter(pretty bool) *JSONFormatter {
	return &JSONFormatter{pretty: pretty}
}

func (f *JSONFormatter) Format(data map[string]interface{}) (string, error) {
	cleaned := stripNullValues(data)
	var output []byte
	var err error

	if f.pretty {
		output, err = json.MarshalIndent(cleaned, "", "  ")
	} else {
		output, err = json.Marshal(cleaned)
	}

	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(output), nil
}

func (f *JSONFormatter) Name() string {
	return "json"
}

// TableFormatter outputs data as a formatted table
type TableFormatter struct{}

// NewTableFormatter creates a table formatter
func NewTableFormatter() *TableFormatter {
	return &TableFormatter{}
}

func (f *TableFormatter) Format(data map[string]interface{}) (string, error) {
	if errs, ok := data["errors"].([]interface{}); ok && len(errs) > 0 {
		return formatErrors(errs), nil
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)

	// Get the first key-value pair
	for key, value := range data {
		fmt.Fprintf(w, "## %s\n\n", key)

		if value == nil {
			fmt.Fprint(w, "null\n")
			w.Flush()
			return buf.String(), nil
		}

		switch v := value.(type) {
		case []interface{}:
			formatArrayTable(w, v)
		case map[string]interface{}:
			formatObjectTable(w, v)
		default:
			fmt.Fprintf(w, "Value: %v\n", v)
		}
		break
	}

	w.Flush()
	return buf.String(), nil
}

func (f *TableFormatter) Name() string {
	return "table"
}

// CompactFormatter outputs data in compact form
type CompactFormatter struct{}

// NewCompactFormatter creates a compact formatter
func NewCompactFormatter() *CompactFormatter {
	return &CompactFormatter{}
}

func (f *CompactFormatter) Format(data map[string]interface{}) (string, error) {
	output, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (f *CompactFormatter) Name() string {
	return "compact"
}

// TOONFormatter outputs data in TOON format (Token-Optimized Object Notation)
// Uses the official toon-go library: https://github.com/toon-format/toon-go
// Reduces token usage by 40-60% compared to standard JSON
type TOONFormatter struct{}

// NewTOONFormatter creates a TOON formatter
func NewTOONFormatter() *TOONFormatter {
	return &TOONFormatter{}
}

func (f *TOONFormatter) Format(data map[string]interface{}) (string, error) {
	if errs, ok := data["errors"].([]interface{}); ok && len(errs) > 0 {
		return formatErrors(errs), nil
	}

	// Extract data from GraphQL result (skip errors wrapper if present)
	dataField, ok := data["data"].(map[string]interface{})
	if !ok || dataField == nil {
		dataField = data
	}

	// Encode using official TOON library's MarshalString
	toonOutput, err := toon.MarshalString(dataField)
	if err != nil {
		return "", fmt.Errorf("TOON encoding failed: %w", err)
	}

	return toonOutput, nil
}

func (f *TOONFormatter) Name() string {
	return "toon"
}

// LLMFormatter outputs data in human and LLM-friendly markdown format
type LLMFormatter struct{}

// NewLLMFormatter creates an LLM formatter
func NewLLMFormatter() *LLMFormatter {
	return &LLMFormatter{}
}

func (f *LLMFormatter) Format(data map[string]interface{}) (string, error) {
	var buf strings.Builder

	// Check for errors
	if errs, ok := data["errors"].([]interface{}); ok && len(errs) > 0 {
		fmt.Fprintf(&buf, "## GraphQL Errors\n\n")
		fmt.Fprint(&buf, formatErrors(errs))
		return buf.String(), nil
	}

	// Format data
	dataField, _ := data["data"].(map[string]interface{})
	formatDataAsMarkdown(&buf, dataField)

	return buf.String(), nil
}

func (f *LLMFormatter) Name() string {
	return "llm"
}

// DefaultFormatterRegistry manages available formatters
type DefaultFormatterRegistry struct {
	formatters map[string]Formatter
}

// NewFormatterRegistry creates a new formatter registry with default formatters
func NewFormatterRegistry() *DefaultFormatterRegistry {
	r := &DefaultFormatterRegistry{
		formatters: make(map[string]Formatter),
	}

	// Register default formatters
	r.formatters["json"] = NewJSONFormatter(false)
	r.formatters["json-pretty"] = NewJSONFormatter(true)
	r.formatters["table"] = NewTableFormatter()
	r.formatters["compact"] = NewCompactFormatter()
	r.formatters["toon"] = NewTOONFormatter()
	r.formatters["llm"] = NewLLMFormatter()

	return r
}

func (r *DefaultFormatterRegistry) Register(name string, formatter Formatter) error {
	if _, exists := r.formatters[name]; exists {
		return fmt.Errorf("formatter '%s' already registered", name)
	}
	r.formatters[name] = formatter
	return nil
}

func (r *DefaultFormatterRegistry) Get(name string) (Formatter, error) {
	formatter, ok := r.formatters[name]
	if !ok {
		return nil, fmt.Errorf("formatter '%s' not found", name)
	}
	return formatter, nil
}

func (r *DefaultFormatterRegistry) List() []string {
	var names []string
	for name := range r.formatters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatErrors renders GraphQL errors as human-readable text.
// Used by formatters that are not JSON-based.
func formatErrors(errors []interface{}) string {
	var buf strings.Builder
	for i, e := range errors {
		em, ok := e.(map[string]interface{})
		if !ok {
			fmt.Fprintf(&buf, "Error %d: %v\n", i+1, e)
			continue
		}

		msg, _ := em["message"].(string)
		fmt.Fprintf(&buf, "Error: %s\n", msg)

		if path, ok := em["path"].([]interface{}); ok && len(path) > 0 {
			var parts []string
			for _, p := range path {
				parts = append(parts, fmt.Sprintf("%v", p))
			}
			fmt.Fprintf(&buf, "Path: %s\n", strings.Join(parts, "."))
		}

		if ext, ok := em["extensions"].(map[string]interface{}); ok {
			if code, ok := ext["code"].(string); ok {
				fmt.Fprintf(&buf, "Code: %s\n", code)
			}
			if hint, ok := ext["schemaHint"].(string); ok {
				fmt.Fprintf(&buf, "Schema hint:\n%s", hint)
			}
		}

		if i < len(errors)-1 {
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// Helper functions

func stripNullValues(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		cleaned := make(map[string]interface{})
		for k, v := range val {
			if v == nil {
				continue
			}
			cleaned[k] = stripNullValues(v)
		}
		return cleaned
	case []interface{}:
		cleaned := make([]interface{}, len(val))
		for i, v := range val {
			cleaned[i] = stripNullValues(v)
		}
		return cleaned
	default:
		return v
	}
}

func formatArrayTable(w *tabwriter.Writer, data []interface{}) {
	if len(data) == 0 {
		fmt.Fprint(w, "Empty array\n")
		return
	}

	// Collect all unique field names
	fieldSet := make(map[string]bool)
	var objects []map[string]interface{}

	for _, item := range data {
		if obj, ok := item.(map[string]interface{}); ok {
			objects = append(objects, obj)
			for key := range obj {
				fieldSet[key] = true
			}
		}
	}

	if len(objects) == 0 {
		fmt.Fprint(w, "No objects found in array\n")
		return
	}

	// Convert to sorted slice
	var fields []string
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	// Print header
	for i, field := range fields {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, strings.ToUpper(field))
	}
	fmt.Fprint(w, "\n")

	// Print separator
	for i := range fields {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, strings.Repeat("-", len(fields[i])+2))
	}
	fmt.Fprint(w, "\n")

	// Print data rows
	for _, obj := range objects {
		for i, field := range fields {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, formatTableValue(obj[field]))
		}
		fmt.Fprint(w, "\n")
	}
}

func formatObjectTable(w *tabwriter.Writer, data map[string]interface{}) {
	if len(data) == 0 {
		fmt.Fprint(w, "Empty object\n")
		return
	}

	var keys []string
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fmt.Fprint(w, "FIELD\tVALUE\n")
	fmt.Fprint(w, "-----\t-----\n")

	for _, key := range keys {
		fmt.Fprintf(w, "%s\t%s\n", key, formatTableValue(data[key]))
	}
}

func formatTableValue(value interface{}) string {
	if value == nil {
		return "null"
	}

	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.2f", v)
	case []interface{}:
		if len(v) == 0 {
			return "[]"
		}
		if len(v) == 1 {
			return fmt.Sprintf("[%s]", formatTableValue(v[0]))
		}
		return fmt.Sprintf("[%s, ... +%d more]", formatTableValue(v[0]), len(v)-1)
	case map[string]interface{}:
		if len(v) == 0 {
			return "{}"
		}
		var parts []string
		count := 0
		for key, val := range v {
			if count >= 3 {
				parts = append(parts, "...")
				break
			}
			var valStr string
			switch val := val.(type) {
			case string:
				valStr = val
			case nil:
				valStr = "null"
			case bool, float64:
				valStr = fmt.Sprintf("%v", val)
			case map[string]interface{}:
				valStr = "{...}"
			case []interface{}:
				valStr = "[...]"
			default:
				valStr = fmt.Sprintf("%v", val)
			}
			if len(valStr) > 20 {
				valStr = valStr[:17] + "..."
			}
			parts = append(parts, fmt.Sprintf("%s=%s", key, valStr))
			count++
		}
		return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatDataAsMarkdown(buf *strings.Builder, data map[string]interface{}) {
	for key, value := range data {
		fmt.Fprintf(buf, "## %s\n\n", key)

		if value == nil {
			fmt.Fprint(buf, "null\n")
			continue
		}

		switch v := value.(type) {
		case map[string]interface{}:
			for k, val := range v {
				fmt.Fprintf(buf, "- **%s**: %v\n", k, val)
			}
		case []interface{}:
			for i, item := range v {
				fmt.Fprintf(buf, "%d. %v\n", i+1, item)
			}
		default:
			fmt.Fprintf(buf, "%v\n", v)
		}

		fmt.Fprint(buf, "\n")
	}
}
