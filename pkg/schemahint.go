package gqlcli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

var reQuotedName = regexp.MustCompile(`"([^"]+)"`)

// schemaHintFromSchema renders a compact SDL hint for the type an error message
// refers to, or "" when the message names no type the schema knows.
//
// This mirrors the schemaHint the executed path attaches via enrichErrors, but
// reads the already-parsed schema instead of issuing a __type introspection
// query per error, so it also works with no network and in inline mode.
func schemaHintFromSchema(schema *ast.Schema, message string) string {
	if schema == nil {
		return ""
	}

	target := extractHintTargetFromErrorMsg(message)

	// For "must have a selection of subfields" the extracted name is a field of
	// the *parent* type, not a member of the type being hinted, so filtering by
	// it would surface unrelated members that merely share a substring. The
	// whole type is what the reader needs there anyway.
	if reErrNeedsSubfield.MatchString(message) {
		target.fieldFilter = ""
	}

	def := hintDefinition(schema, target.typeName)
	if def == nil {
		// The per-message patterns only cover the error shapes enrichErrors was
		// written for. Falling back to any quoted name that resolves to a type
		// covers the rest — enum values, fragment spreads, type conditions —
		// without a pattern per message.
		def, target.fieldFilter = hintDefinitionFromMessage(schema, message), ""
	}
	if def == nil {
		return ""
	}

	// Scalars and unions have no members to narrow, so a "closest matches"
	// header over them would promise a filtering that never happened.
	if target.fieldFilter != "" && hasHintMembers(def.Kind) {
		if filtered := definitionSDL(def, target.fieldFilter); filtered != "" {
			return truncateHint("# Closest matches\n" + filtered)
		}
	}
	return truncateHint(definitionSDL(def, ""))
}

func hintDefinition(schema *ast.Schema, name string) *ast.Definition {
	name = strings.Trim(strings.TrimSpace(name), "[]!")
	if name == "" || strings.HasPrefix(name, "__") || isBuiltInScalar(name) {
		return nil
	}
	return schema.Types[name]
}

func hintDefinitionFromMessage(schema *ast.Schema, message string) *ast.Definition {
	for _, match := range reQuotedName.FindAllStringSubmatch(message, -1) {
		if def := hintDefinition(schema, match[1]); def != nil {
			return def
		}
	}
	return nil
}

// definitionSDL renders one type as compact SDL. A non-empty fieldFilter keeps
// only the members whose names contain it, and returns "" when none do, so the
// caller can fall back to the whole type.
func definitionSDL(def *ast.Definition, fieldFilter string) string {
	switch def.Kind {
	case ast.Scalar:
		return fmt.Sprintf("scalar %s\n", def.Name)

	case ast.Union:
		if len(def.Types) == 0 {
			return fmt.Sprintf("union %s\n", def.Name)
		}
		return fmt.Sprintf("union %s = %s\n", def.Name, strings.Join(def.Types, " | "))

	case ast.Enum:
		var values []string
		for _, v := range def.EnumValues {
			if matchesHintFilter(v.Name, fieldFilter) {
				values = append(values, "  "+v.Name+"\n")
			}
		}
		if len(values) == 0 {
			return ""
		}
		return fmt.Sprintf("enum %s {\n%s}\n", def.Name, strings.Join(values, ""))

	default:
		keyword := "type"
		switch def.Kind {
		case ast.Interface:
			keyword = "interface"
		case ast.InputObject:
			keyword = "input"
		}

		header := keyword + " " + def.Name
		if len(def.Interfaces) > 0 {
			header += " implements " + strings.Join(def.Interfaces, " & ")
		}

		var fields []string
		for _, f := range def.Fields {
			if strings.HasPrefix(f.Name, "__") || !matchesHintFilter(f.Name, fieldFilter) {
				continue
			}
			fields = append(fields, "  "+f.Name+hintArgs(f.Arguments)+": "+f.Type.String()+"\n")
		}
		if len(fields) == 0 {
			return ""
		}
		return fmt.Sprintf("%s {\n%s}\n", header, strings.Join(fields, ""))
	}
}

// hintArgs renders an argument list. Unlike the executed path's hints, these
// keep argument signatures: an unknown-argument error is unreadable without
// them.
func hintArgs(args ast.ArgumentDefinitionList) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		part := a.Name + ": " + a.Type.String()
		if a.DefaultValue != nil {
			part += " = " + a.DefaultValue.String()
		}
		parts = append(parts, part)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// hasHintMembers reports whether a kind has members a filter could narrow.
func hasHintMembers(kind ast.DefinitionKind) bool {
	switch kind {
	case ast.Object, ast.Interface, ast.InputObject, ast.Enum:
		return true
	default:
		return false
	}
}

func matchesHintFilter(name, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(filter))
}
