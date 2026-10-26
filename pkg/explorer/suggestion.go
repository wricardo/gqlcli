package explorer

import (
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

// suggestedDocument turns a directed output path into a single executable
// selection. Not every schema relationship corresponds to a selectable field.
func (r *Resolver) suggestedDocument(path *model.SchemaPath) (*string, error) {
	if r == nil || r.Schema == nil || r.Index == nil || path == nil || path.From == nil || path.To == nil {
		return nil, nil
	}

	current := r.element(path.From.GetCoordinate())
	if current == nil {
		return nil, nil
	}
	start := 0
	if current.Kind == "TYPE" {
		// Paths can also start at the root type, before its operation field.
		if len(path.Segments) == 0 || path.Segments[0] == nil || path.Segments[0].Edge == nil {
			return nil, nil
		}
		first := r.edgeByID(path.Segments[0].Edge.ID)
		if first == nil || first.Source != current || first.Target == nil || first.Kind != "DECLARES_FIELD" || r.operation(first.Target.Coordinate) == nil {
			return nil, nil
		}
		current = first.Target
		start = 1
	}
	operation := r.operation(current.Coordinate)
	if operation == nil || current.Kind != "FIELD" || current.Field == nil {
		return nil, nil
	}

	type selection struct {
		field    *graph.Element
		fragment string
	}
	selected := []selection{{field: current}}
	fragment := ""
	for _, segment := range path.Segments[start:] {
		if segment == nil || segment.Edge == nil {
			return nil, nil
		}
		edge := r.edgeByID(segment.Edge.ID)
		if edge == nil || edge.Source != current || edge.Target == nil {
			return nil, nil
		}
		switch edge.Kind {
		case "RETURNS":
			if current.Kind != "FIELD" || current.Field == nil || edge.Target.Kind != "TYPE" || current.Field.Type == nil || current.Field.Type.Name() != edge.Target.Name {
				return nil, nil
			}
		case "UNION_MEMBER", "POSSIBLE_TYPE":
			if current.Kind != "TYPE" || current.Definition == nil || edge.Target.Kind != "TYPE" || edge.Target.Definition == nil || edge.Target.Definition.Kind != ast.Object || fragment != "" {
				return nil, nil
			}
			if edge.Kind == "UNION_MEMBER" && current.Definition.Kind != ast.Union || edge.Kind == "POSSIBLE_TYPE" && current.Definition.Kind != ast.Interface {
				return nil, nil
			}
			fragment = edge.Target.Name
		case "DECLARES_FIELD":
			if current.Kind != "TYPE" || current.Definition == nil || edge.Target.Kind != "FIELD" || edge.Target.Field == nil || edge.Target.Parent != current.Definition {
				return nil, nil
			}
			selected = append(selected, selection{field: edge.Target, fragment: fragment})
			fragment = ""
		default:
			return nil, nil
		}
		current = edge.Target
	}
	if current.Kind != "FIELD" || current.Coordinate != path.To.GetCoordinate() || fragment != "" || current.Field == nil || current.Field.Type == nil {
		return nil, nil
	}

	operationType := ""
	switch operation.Kind {
	case model.OperationKindQuery:
		operationType = "query"
	case model.OperationKindMutation:
		operationType = "mutation"
	case model.OperationKindSubscription:
		operationType = "subscription"
	default:
		return nil, nil
	}

	var variables, fields strings.Builder
	for i, step := range selected {
		if i != 0 {
			fields.WriteString(" { ")
			if step.fragment != "" {
				fields.WriteString("... on ")
				fields.WriteString(step.fragment)
				fields.WriteString(" { ")
			}
		}
		fields.WriteString(step.field.Name)
		firstArg := true
		for _, arg := range step.field.Field.Arguments {
			if arg == nil || arg.Type == nil || !arg.Type.NonNull || arg.DefaultValue != nil {
				continue
			}
			name := "v" + strconv.Itoa(i) + "_" + arg.Name
			if firstArg {
				fields.WriteByte('(')
				firstArg = false
			} else {
				fields.WriteString(", ")
			}
			fields.WriteString(arg.Name)
			fields.WriteString(": $")
			fields.WriteString(name)
			if variables.Len() != 0 {
				variables.WriteString(", ")
			}
			variables.WriteByte('$')
			variables.WriteString(name)
			variables.WriteString(": ")
			variables.WriteString(arg.Type.String())
		}
		if !firstArg {
			fields.WriteByte(')')
		}
	}
	lastType := r.Schema.Types[current.Field.Type.Name()]
	if lastType == nil {
		return nil, nil
	}
	switch lastType.Kind {
	case ast.Object, ast.Interface, ast.Union:
		fields.WriteString(" { __typename }")
	case ast.Scalar, ast.Enum:
	default:
		return nil, nil
	}
	for i := len(selected) - 1; i > 0; i-- {
		if selected[i].fragment != "" {
			fields.WriteString(" }")
		}
		fields.WriteString(" }")
	}

	var document strings.Builder
	document.WriteString(operationType)
	if variables.Len() != 0 {
		document.WriteByte('(')
		document.WriteString(variables.String())
		document.WriteByte(')')
	}
	document.WriteString(" { ")
	document.WriteString(fields.String())
	document.WriteString(" }")
	result := document.String()
	if !r.validate(result).Valid {
		return nil, nil
	}
	return &result, nil
}
