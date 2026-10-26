package explorer

import (
	"regexp"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

var quotedIdentifier = regexp.MustCompile(`"([_A-Za-z][_0-9A-Za-z]*)"`)
var unknownArgumentField = regexp.MustCompile(`Unknown argument "([^"]+)" on field "([^"]+)"`)
var targetValidationRules = rules.NewDefaultRules()

type validationSite struct {
	line, column int
	coord, input string
	typ          *ast.Type
}

func (r *Resolver) validationSites(document string) []validationSite {
	query, err := parser.ParseQuery(&ast.Source{Input: document})
	if err != nil {
		return nil
	}
	sites := []validationSite{}
	visited := map[string]bool{}
	var visit func(ast.SelectionSet, *ast.Definition)
	visit = func(selections ast.SelectionSet, parent *ast.Definition) {
		for _, selection := range selections {
			switch item := selection.(type) {
			case *ast.Field:
				if parent == nil {
					continue
				}
				coord := parent.Name + "." + item.Name
				field := parent.Fields.ForName(item.Name)
				if item.Position != nil {
					site := validationSite{line: item.Position.Line, column: item.Position.Column, coord: coord}
					if field != nil {
						site.typ = field.Type
					}
					sites = append(sites, site)
				}
				if field == nil {
					continue
				}
				for _, arg := range item.Arguments {
					if arg.Position == nil {
						continue
					}
					def := field.Arguments.ForName(arg.Name)
					site := validationSite{line: arg.Position.Line, column: arg.Position.Column, coord: coord, input: coord + "(" + arg.Name + ":)"}
					if def != nil {
						site.typ = def.Type
					}
					sites = append(sites, site)
				}
				visit(item.SelectionSet, r.Schema.Types[field.Type.Name()])
			case *ast.InlineFragment:
				next := parent
				if item.TypeCondition != "" {
					next = r.Schema.Types[item.TypeCondition]
				}
				visit(item.SelectionSet, next)
			case *ast.FragmentSpread:
				if visited[item.Name] {
					continue
				}
				visited[item.Name] = true
				if fragment := query.Fragments.ForName(item.Name); fragment != nil {
					visit(fragment.SelectionSet, r.Schema.Types[fragment.TypeCondition])
				}
			}
		}
	}
	for _, op := range query.Operations {
		root := r.Schema.Query
		switch op.Operation {
		case ast.Mutation:
			root = r.Schema.Mutation
		case ast.Subscription:
			root = r.Schema.Subscription
		}
		visit(op.SelectionSet, root)
	}
	for _, fragment := range query.Fragments {
		if !visited[fragment.Name] {
			visited[fragment.Name] = true
			visit(fragment.SelectionSet, r.Schema.Types[fragment.TypeCondition])
		}
	}
	return sites
}

func (r *Resolver) validate(document string) *model.ValidationResult {
	_, errs := gqlparser.LoadQueryWithRules(r.Schema, document, targetValidationRules)
	result := &model.ValidationResult{Valid: len(errs) == 0, Errors: make([]*model.ValidationDiagnostic, 0, len(errs))}
	if len(errs) == 0 {
		return result
	}
	sites := r.validationSites(document)
	for _, e := range errs {
		d := &model.ValidationDiagnostic{Message: e.Message, Rule: text(e.Rule), Locations: make([]*model.SourceLocation, 0, len(e.Locations)), Suggestions: []*model.ValidationSuggestion{}}
		for _, loc := range e.Locations {
			d.Locations = append(d.Locations, &model.SourceLocation{Line: loc.Line, Column: loc.Column})
		}
		var site *validationSite
		if len(e.Locations) > 0 {
			loc := e.Locations[0]
			for i := range sites {
				candidate := &sites[i]
				if (candidate.line < loc.Line || candidate.line == loc.Line && candidate.column <= loc.Column) &&
					(site == nil || candidate.line > site.line || candidate.line == site.line && candidate.column > site.column) {
					site = candidate
				}
			}
		}
		if site != nil {
			d.Coordinate = text(site.coord)
			d.Field = r.schemaField(site.coord)
			if site.input != "" {
				d.Coordinate = text(site.input)
				d.InputValue = r.schemaInput(site.input)
			}
			if site.typ != nil {
				d.ExpectedType, _ = r.typeRef(site.typ)
			}
		}
		r.enrichDiagnostic(d)
		result.Errors = append(result.Errors, d)
	}
	return result
}

func (r *Resolver) enrichDiagnostic(d *model.ValidationDiagnostic) {
	// Required arguments and unknown input-object fields identify schema coordinates
	// even when the offending value is absent from the document's AST.
	names := quotedIdentifier.FindAllStringSubmatch(d.Message, -1)
	if parts := unknownArgumentField.FindStringSubmatch(d.Message); len(parts) == 3 {
		coord := parts[2] + "(" + parts[1] + ":)"
		d.Coordinate = &coord
		d.Field = r.schemaField(parts[2])
		d.InputValue = nil
		d.ExpectedType = nil
	}
	if d.Coordinate != nil && strings.HasPrefix(d.Message, "Field ") && strings.Contains(d.Message, " is required, but it was not provided") && len(names) > 1 {
		coord := *d.Coordinate + "(" + names[1][1] + ":)"
		if value := r.schemaInput(coord); value != nil {
			d.Coordinate = &coord
			d.InputValue = value
			d.ExpectedType, _ = r.inputType(coord)
		}
	}
	if strings.Contains(d.Message, "is not defined by type") && len(names) > 1 {
		coord := names[1][1] + "." + names[0][1]
		d.Coordinate = &coord
		d.Field = nil
		d.InputValue = r.schemaInput(coord)
		d.ExpectedType = nil
	}
	if d.Coordinate == nil {
		return
	}
	marker := strings.Index(d.Message, "Did you mean")
	if marker < 0 {
		return
	}
	coord := *d.Coordinate
	for _, name := range quotedIdentifier.FindAllStringSubmatch(d.Message[marker:], -1) {
		if strings.Contains(coord, "(") {
			parent, _, _ := strings.Cut(coord, "(")
			if v := r.schemaInput(parent + "(" + name[1] + ":)"); v != nil {
				d.Suggestions = append(d.Suggestions, &model.ValidationSuggestion{Message: "Use " + v.Name, ReplacementInputValue: v})
			}
		} else {
			parent, _, ok := strings.Cut(coord, ".")
			if !ok {
				continue
			}
			if f := r.schemaField(parent + "." + name[1]); f != nil {
				d.Suggestions = append(d.Suggestions, &model.ValidationSuggestion{Message: "Use " + f.Name, ReplacementField: f})
			} else if v := r.schemaInput(parent + "." + name[1]); v != nil {
				d.Suggestions = append(d.Suggestions, &model.ValidationSuggestion{Message: "Use " + v.Name, ReplacementInputValue: v})
			}
		}
	}
}
