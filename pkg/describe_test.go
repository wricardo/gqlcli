package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestBuildDescribeQueryIncludesPossibleTypes(t *testing.T) {
	query := buildDescribeQuery("SearchResult")
	if !strings.Contains(query, "possibleTypes { ...TypeRef }") {
		t.Fatalf("describe query must request possibleTypes for union/interface recursion, got: %s", query)
	}
}

func TestDescribeWithDepthRecursesThroughPossibleTypes(t *testing.T) {
	typeNamePattern := regexp.MustCompile(`__type\(name:\s*"([^"]+)"\)`)

	typeData := map[string]map[string]interface{}{
		"SearchResult": {
			"name": "SearchResult",
			"kind": "UNION",
			"possibleTypes": []interface{}{
				map[string]interface{}{"kind": "OBJECT", "name": "Citizen", "ofType": nil},
				map[string]interface{}{"kind": "OBJECT", "name": "City", "ofType": nil},
			},
		},
		"Citizen": {
			"name": "Citizen",
			"kind": "OBJECT",
			"fields": []interface{}{
				map[string]interface{}{
					"name": "name",
					"type": map[string]interface{}{"kind": "SCALAR", "name": "String", "ofType": nil},
					"args": []interface{}{},
				},
			},
		},
		"City": {
			"name": "City",
			"kind": "OBJECT",
			"fields": []interface{}{
				map[string]interface{}{
					"name": "title",
					"type": map[string]interface{}{"kind": "SCALAR", "name": "String", "ofType": nil},
					"args": []interface{}{},
				},
			},
		},
	}

	d := &Describer{}
	d.exec = func(_ context.Context, query string, _ map[string]interface{}) (json.RawMessage, error) {
		matches := typeNamePattern.FindStringSubmatch(query)
		if len(matches) != 2 {
			return nil, fmt.Errorf("failed to parse type name from query: %s", query)
		}
		typeName := matches[1]

		rawType, ok := typeData[typeName]
		if !ok {
			return nil, fmt.Errorf("unexpected type requested: %s", typeName)
		}

		rawJSON, err := json.Marshal(rawType)
		if err != nil {
			return nil, err
		}
		var responseType map[string]interface{}
		if err := json.Unmarshal(rawJSON, &responseType); err != nil {
			return nil, err
		}

		if !strings.Contains(query, "possibleTypes") {
			delete(responseType, "possibleTypes")
		}

		return json.Marshal(map[string]interface{}{
			"data": map[string]interface{}{
				"__type": responseType,
			},
		})
	}

	sdl, err := d.DescribeWithDepth(context.Background(), "SearchResult", false, false, 1)
	if err != nil {
		t.Fatalf("DescribeWithDepth returned error: %v", err)
	}

	if !strings.Contains(sdl, "type Citizen {") {
		t.Fatalf("depth recursion should include possible type Citizen, got: %s", sdl)
	}
	if !strings.Contains(sdl, "type City {") {
		t.Fatalf("depth recursion should include possible type City, got: %s", sdl)
	}
}
