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
