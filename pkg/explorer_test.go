package gqlcli

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSchemaExplorerCustomRootsAndTypeWrappers(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/explorer/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	explorer, err := NewSchemaExplorerFromSDL(string(fixture))
	if err != nil {
		t.Fatal(err)
	}
	result, err := explorer.Execute(context.Background(), `{ overview { queryRoot { name } mutationCount subscriptionCount } operation(coordinate:"RootQuery.leads") { coordinate returnType { signature isNonNull ofType { signature isList ofType { signature isNonNull namedType { name } } } } } element(coordinate:"RootQuery.lead(id:)") { __typename coordinate } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result["errors"]; ok {
		t.Fatalf("explorer GraphQL errors: %+v", result["errors"])
	}
	data := result["data"].(map[string]interface{})
	overview := data["overview"].(map[string]interface{})
	if overview["queryRoot"].(map[string]interface{})["name"] != "RootQuery" {
		t.Fatalf("wrong custom query root: %v", overview)
	}
	if overview["mutationCount"] != float64(1) || overview["subscriptionCount"] != float64(1) {
		t.Fatalf("wrong operation counts: %v", overview)
	}
	op := data["operation"].(map[string]interface{})
	if op["returnType"].(map[string]interface{})["signature"] != "[Lead!]!" {
		t.Fatalf("lost type wrappers: %v", op)
	}
	arg := data["element"].(map[string]interface{})
	if arg["__typename"] != "SchemaInputValue" {
		t.Fatalf("wrong element kind: %v", arg)
	}
}

func TestSchemaExplorerSDLDescriptionOption(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/explorer/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	explorer, err := NewSchemaExplorerFromSDL(string(fixture))
	if err != nil {
		t.Fatal(err)
	}
	result, err := explorer.Execute(context.Background(), `{
		plain: operation(coordinate:"RootQuery.lead") { sdl }
		documented: operation(coordinate:"RootQuery.lead") { sdl(includeDescription:true) }
		explicit: operation(coordinate:"RootQuery.lead") { sdl(includeDescription:false) }
		kind: type(name:"Person") { sdl sdlWithDocs:sdl(includeDescription:true) }
		field: element(coordinate:"RootQuery.lead(id:)") { sdl sdlWithDocs:sdl(includeDescription:true) }
		enumValue: element(coordinate:"LeadStatus.ARCHIVED") { sdl }
	}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if errors, ok := result["errors"]; ok {
		t.Fatalf("explorer GraphQL errors: %v", errors)
	}
	data := result["data"].(map[string]interface{})
	plain := data["plain"].(map[string]interface{})["sdl"]
	if plain != "lead(id: ID!): Lead" || data["explicit"].(map[string]interface{})["sdl"] != plain {
		t.Fatalf("undocumented SDL: %v", data)
	}
	documented := data["documented"].(map[string]interface{})["sdl"].(string)
	if !strings.Contains(documented, `"Target lead identifier."`) || !strings.Contains(documented, "lead(\n") {
		t.Fatalf("argument description missing: %s", documented)
	}
	kind := data["kind"].(map[string]interface{})
	if kind["sdl"] != "interface Person {\n  id: ID!\n  name: String!\n}" || !strings.Contains(kind["sdlWithDocs"].(string), `"A person represented in an account."`) {
		t.Fatalf("type description option: %v", kind)
	}
	arg := data["field"].(map[string]interface{})
	if arg["sdl"] != "id: ID!" || !strings.Contains(arg["sdlWithDocs"].(string), `"Target lead identifier."`) {
		t.Fatalf("input value description option: %v", arg)
	}
	if data["enumValue"].(map[string]interface{})["sdl"] != `ARCHIVED @deprecated(reason: "retired")` {
		t.Fatalf("deprecation lost: %v", data["enumValue"])
	}
}

func TestSchemaExplorerTargetValidationIsLocal(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/explorer/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	explorer, err := NewSchemaExplorerFromSDL(string(fixture))
	if err != nil {
		t.Fatal(err)
	}
	result, err := explorer.Execute(context.Background(), `{ validate(document:"{ lead(id: \"1\") { emaill } }") { valid errors { message locations { line column } suggestions { replacementField { coordinate } } } } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if errors, ok := result["errors"]; ok {
		t.Fatalf("explorer GraphQL errors: %v", errors)
	}
	value := result["data"].(map[string]interface{})["validate"].(map[string]interface{})
	if value["valid"] != false || len(value["errors"].([]interface{})) == 0 {
		t.Fatalf("expected validation diagnostic: %v", value)
	}
	message := value["errors"].([]interface{})[0].(map[string]interface{})["message"].(string)
	if !strings.Contains(message, "emaill") {
		t.Fatalf("lost validation error: %v", value)
	}
}
