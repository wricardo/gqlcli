package gqlcli

import (
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// roundTripSDL exercises every construct the emitter has to get right: root
// types under non-default names, an object implementing two interfaces, a
// union, a deprecated enum value and field, an input object with defaults, a
// custom scalar, a repeatable custom directive, a stacked list, and a non-null
// argument that carries a default.
const roundTripSDL = `
schema { query: RootQuery mutation: RootMutation }

directive @tag(name: String!, weight: Int = 1) repeatable on FIELD | FIELD_DEFINITION

"A point in time."
scalar DateTime

interface Node { id: ID! }

interface Timestamped { createdAt: DateTime }

"""
A person.
Spans lines.
"""
type User implements Node & Timestamped {
  id: ID!
  createdAt: DateTime
  name: String!
  nickname: String @deprecated(reason: "use name")
  scores: [[Int!]!]!
  role(fallback: Role! = GUEST): Role!
}

type Team implements Node {
  id: ID!
  members(filter: MemberFilter = {limit: 10}): [User!]!
}

union SearchResult = User | Team

enum Role {
  ADMIN
  GUEST
  ROBOT @deprecated(reason: "retired")
}

input MemberFilter {
  limit: Int = 10
  role: Role
  q: String
}

type RootQuery {
  node(id: ID!): Node
  search(term: String!): [SearchResult!]!
  me: User
}

type RootMutation {
  rename(id: ID!, name: String!): User
}
`

// introspectSchema produces an introspection result the way a gqlgen server
// would: by walking gqlgen's own introspection wrappers. Using the real
// server-side implementation keeps this an independent oracle rather than a
// second copy of the emitter's assumptions.
func introspectSchema(schema *ast.Schema) map[string]interface{} {
	s := introspection.WrapSchema(schema)

	types := s.Types()
	typeList := make([]interface{}, 0, len(types))
	for i := range types {
		typeList = append(typeList, introspectionTypeMap(&types[i]))
	}

	directives := s.Directives()
	directiveList := make([]interface{}, 0, len(directives))
	for i := range directives {
		d := &directives[i]
		locations := make([]interface{}, 0, len(d.Locations))
		for _, l := range d.Locations {
			locations = append(locations, l)
		}
		dm := map[string]interface{}{
			"name":         d.Name,
			"locations":    locations,
			"isRepeatable": d.IsRepeatable,
			"args":         introspectionInputValues(d.Args),
		}
		if desc := d.Description(); desc != nil && *desc != "" {
			dm["description"] = *desc
		}
		directiveList = append(directiveList, dm)
	}

	root := map[string]interface{}{
		"types":      typeList,
		"directives": directiveList,
	}
	for field, t := range map[string]*introspection.Type{
		"queryType":        s.QueryType(),
		"mutationType":     s.MutationType(),
		"subscriptionType": s.SubscriptionType(),
	} {
		if t == nil {
			continue
		}
		if name := t.Name(); name != nil {
			root[field] = map[string]interface{}{"name": *name}
		}
	}

	return map[string]interface{}{"data": map[string]interface{}{"__schema": root}}
}

func introspectionTypeMap(t *introspection.Type) map[string]interface{} {
	m := map[string]interface{}{"kind": t.Kind()}
	if name := t.Name(); name != nil {
		m["name"] = *name
	}
	if desc := t.Description(); desc != nil && *desc != "" {
		m["description"] = *desc
	}

	if fields := t.Fields(true); len(fields) > 0 {
		list := make([]interface{}, 0, len(fields))
		for i := range fields {
			f := &fields[i]
			fm := map[string]interface{}{
				"name":         f.Name,
				"type":         introspectionTypeRefMap(f.Type),
				"args":         introspectionInputValues(f.Args),
				"isDeprecated": f.IsDeprecated(),
			}
			if desc := f.Description(); desc != nil && *desc != "" {
				fm["description"] = *desc
			}
			if reason := f.DeprecationReason(); reason != nil {
				fm["deprecationReason"] = *reason
			}
			list = append(list, fm)
		}
		m["fields"] = list
	}
	if inputFields := t.InputFields(); len(inputFields) > 0 {
		m["inputFields"] = introspectionInputValues(inputFields)
	}
	if interfaces := t.Interfaces(); len(interfaces) > 0 {
		list := make([]interface{}, 0, len(interfaces))
		for i := range interfaces {
			list = append(list, introspectionTypeRefMap(&interfaces[i]))
		}
		m["interfaces"] = list
	}
	if values := t.EnumValues(true); len(values) > 0 {
		list := make([]interface{}, 0, len(values))
		for i := range values {
			v := &values[i]
			vm := map[string]interface{}{"name": v.Name, "isDeprecated": v.IsDeprecated()}
			if desc := v.Description(); desc != nil && *desc != "" {
				vm["description"] = *desc
			}
			if reason := v.DeprecationReason(); reason != nil {
				vm["deprecationReason"] = *reason
			}
			list = append(list, vm)
		}
		m["enumValues"] = list
	}
	if possible := t.PossibleTypes(); len(possible) > 0 {
		list := make([]interface{}, 0, len(possible))
		for i := range possible {
			list = append(list, introspectionTypeRefMap(&possible[i]))
		}
		m["possibleTypes"] = list
	}
	return m
}

func introspectionInputValues(values []introspection.InputValue) []interface{} {
	list := make([]interface{}, 0, len(values))
	for i := range values {
		v := &values[i]
		m := map[string]interface{}{"name": v.Name, "type": introspectionTypeRefMap(v.Type)}
		if v.DefaultValue != nil {
			m["defaultValue"] = *v.DefaultValue
		}
		if desc := v.Description(); desc != nil && *desc != "" {
			m["description"] = *desc
		}
		list = append(list, m)
	}
	return list
}

func introspectionTypeRefMap(t *introspection.Type) map[string]interface{} {
	if t == nil {
		return nil
	}
	m := map[string]interface{}{"kind": t.Kind()}
	if name := t.Name(); name != nil {
		m["name"] = *name
	}
	if of := t.OfType(); of != nil {
		m["ofType"] = introspectionTypeRefMap(of)
	}
	return m
}

func TestSchemaFromIntrospectionRoundTrip(t *testing.T) {
	original := gqlparser.MustLoadSchema(&ast.Source{Name: "round-trip", Input: roundTripSDL})

	sdl, err := SchemaSDL(introspectSchema(original))
	if err != nil {
		t.Fatalf("SchemaSDL: %v", err)
	}
	rebuilt, err := SchemaFromIntrospection(introspectSchema(original))
	if err != nil {
		t.Fatalf("SchemaFromIntrospection: %v\n--- SDL ---\n%s", err, sdl)
	}

	// Root operations must survive under their non-default names.
	if rebuilt.Query == nil || rebuilt.Query.Name != "RootQuery" {
		t.Errorf("rebuilt query root = %v, want RootQuery", rebuilt.Query)
	}
	if rebuilt.Mutation == nil || rebuilt.Mutation.Name != "RootMutation" {
		t.Errorf("rebuilt mutation root = %v, want RootMutation", rebuilt.Mutation)
	}
	if rebuilt.Subscription != nil {
		t.Errorf("rebuilt subscription root = %v, want none", rebuilt.Subscription)
	}

	// The emitter must not redeclare what the prelude already provides.
	for _, unwanted := range []string{"scalar String", "scalar Int", "scalar ID", "directive @deprecated", "directive @skip"} {
		if strings.Contains(sdl, unwanted) {
			t.Errorf("SDL redeclares a builtin (%q):\n%s", unwanted, sdl)
		}
	}
	if strings.Contains(sdl, "__") {
		t.Errorf("SDL leaked a reserved __ name:\n%s", sdl)
	}

	for _, want := range []string{
		"type User implements Node & Timestamped",
		"union SearchResult = User | Team",
		"scalar DateTime",
		"directive @tag",
		"repeatable on",
		"scores: [[Int!]!]!",
		"fallback: Role! = GUEST",
		"limit: Int = 10",
		`@deprecated(reason: "use name")`,
		`@deprecated(reason: "retired")`,
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("SDL missing %q:\n%s", want, sdl)
		}
	}

	documents := []struct {
		name  string
		doc   string
		valid bool
	}{
		{"valid selection", `{ me { id name createdAt } }`, true},
		{"interface and fragments", `{ node(id: "1") { id ... on User { name } ... on Team { members { id } } } }`, true},
		{"union members", `{ search(term: "x") { ... on User { name } ... on Team { id } } }`, true},
		{"custom directive", `{ me { name @tag(name: "pii") } }`, true},
		{"defaulted non-null argument omitted", `{ me { role } }`, true},
		{"input object default omitted", `{ node(id: "1") { ... on Team { members { id } } } }`, true},
		{"nested list variable", `query ($s: [[Int!]!]!) { me { name } search(term: "x") { __typename } }`, false},
		{"deprecated field still selectable", `{ me { nickname } }`, true},
		{"enum literal argument", `{ me { role(fallback: ROBOT) } }`, true},
		{"input object literal argument", `{ node(id: "1") { ... on Team { members(filter: {limit: 2, role: ADMIN}) { id } } } }`, true},
		{"variables", `query ($id: ID!, $f: MemberFilter) { node(id: $id) { ... on Team { members(filter: $f) { id } } } }`, true},
		{"mutation", `mutation { rename(id: "1", name: "x") { id } }`, true},

		{"unknown field", `{ me { nope } }`, false},
		{"unknown argument", `{ me { name(nope: 1) } }`, false},
		{"wrong argument type", `{ node(id: 1.5) { id } }`, false},
		{"missing required argument", `{ node { id } }`, false},
		{"undeclared variable", `{ node(id: $id) { id } }`, false},
		{"unused variable", `query ($unused: ID!) { me { id } }`, false},
		{"fragment on impossible type", `{ me { ... on Team { id } } }`, false},
		{"unknown type in fragment", `{ me { ... on Nope { id } } }`, false},
		{"bad enum literal", `{ me { role(fallback: NOPE) } }`, false},
		{"unknown input field", `{ node(id: "1") { ... on Team { members(filter: {nope: 1}) { id } } } }`, false},
		{"scalar with subselection", `{ me { name { nope } } }`, false},
		{"missing subselection", `{ me }`, false},
		{"unknown directive", `{ me { name @nope } }`, false},
		{"syntax error", `{ me { name `, false},
	}

	for _, tc := range documents {
		t.Run(tc.name, func(t *testing.T) {
			originalErrs := NewSchemaValidator(original).Validate(tc.doc)
			rebuiltErrs := NewSchemaValidator(rebuilt).Validate(tc.doc)

			if originalErrs.Valid != tc.valid {
				t.Fatalf("original schema: valid = %v, want %v (errors: %v)", originalErrs.Valid, tc.valid, originalErrs.Errors)
			}
			if rebuiltErrs.Valid != originalErrs.Valid {
				t.Errorf("rebuilt schema disagrees with original: valid = %v, want %v (errors: %v)",
					rebuiltErrs.Valid, originalErrs.Valid, rebuiltErrs.Errors)
			}
		})
	}
}

func TestSchemaSDLRejectsBadIntrospection(t *testing.T) {
	cases := []struct {
		name        string
		payload     map[string]interface{}
		wantMessage string
	}{
		{
			name:        "empty",
			payload:     nil,
			wantMessage: "empty",
		},
		{
			name: "graphql errors",
			payload: map[string]interface{}{
				"errors": []interface{}{map[string]interface{}{"message": "introspection is disabled"}},
			},
			wantMessage: "introspection is disabled",
		},
		{
			name:        "no schema",
			payload:     map[string]interface{}{"data": map[string]interface{}{"me": nil}},
			wantMessage: "no __schema",
		},
		{
			name:        "no types",
			payload:     map[string]interface{}{"data": map[string]interface{}{"__schema": map[string]interface{}{}}},
			wantMessage: "no types",
		},
		{
			// A truncated ofType chain is what a too-shallow introspection query
			// produces; silently emitting a nameless type would be worse.
			name: "truncated type reference",
			payload: map[string]interface{}{"data": map[string]interface{}{"__schema": map[string]interface{}{
				"queryType": map[string]interface{}{"name": "Query"},
				"types": []interface{}{map[string]interface{}{
					"kind": "OBJECT",
					"name": "Query",
					"fields": []interface{}{map[string]interface{}{
						"name": "deep",
						"type": map[string]interface{}{"kind": "LIST", "ofType": map[string]interface{}{"kind": "LIST"}},
					}},
				}},
			}}},
			wantMessage: "too shallow",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SchemaSDL(tc.payload)
			if err == nil {
				t.Fatal("SchemaSDL succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantMessage)
			}
		})
	}
}

// A bare __schema map is accepted alongside the full response envelope.
func TestSchemaSDLAcceptsBareSchemaMap(t *testing.T) {
	original := gqlparser.MustLoadSchema(&ast.Source{Name: "bare", Input: roundTripSDL})
	envelope := introspectSchema(original)
	root := envelope["data"].(map[string]interface{})["__schema"].(map[string]interface{})

	if _, err := SchemaFromIntrospection(root); err != nil {
		t.Fatalf("SchemaFromIntrospection with a bare __schema map: %v", err)
	}
}
