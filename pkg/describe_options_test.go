package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeIntrospector serves canned introspection envelopes and counts fetches, so
// tests can assert on the Describer's caching.
type fakeIntrospector struct {
	types map[string]string
	calls map[string]int
}

func (f *fakeIntrospector) exec(_ context.Context, query string, _ map[string]interface{}) (json.RawMessage, error) {
	if strings.Contains(query, "__schema") {
		f.calls["__schema"]++
		types := make([]json.RawMessage, 0, len(f.types))
		for _, body := range f.types {
			types = append(types, json.RawMessage(body))
		}
		return json.RawMessage(fmt.Sprintf(`{"data":{"__schema":{"queryType":{"name":"Query"},"mutationType":{"name":"Mutation"},"types":[%s]}}}`,
			strings.Join(rawMessagesToStrings(types), ","))), nil
	}

	name := ""
	if i := strings.Index(query, `__type(name: "`); i >= 0 {
		rest := query[i+len(`__type(name: "`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			name = rest[:j]
		}
	}
	f.calls[name]++
	body, ok := f.types[name]
	if !ok {
		return json.RawMessage(`{"data":{"__type":null}}`), nil
	}
	return json.RawMessage(fmt.Sprintf(`{"data":{"__type":%s}}`, body)), nil
}

func rawMessagesToStrings(parts []json.RawMessage) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	return out
}

func newFakeDescriber() (*Describer, *fakeIntrospector) {
	f := &fakeIntrospector{calls: map[string]int{}, types: map[string]string{
		"Query": `{
			"name":"Query","kind":"OBJECT",
			"fields":[
				{"name":"smsCampaigns","type":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaignList"}},"args":[{"name":"limit","type":{"kind":"SCALAR","name":"Int"}}]},
				{"name":"smsCampaignPreview","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]},
				{"name":"smsCampaignBySlug","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[{"name":"slug","type":{"kind":"SCALAR","name":"String"}}]},
				{"name":"smsCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[{"name":"id","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}}}]},
				{"name":"emailCampaigns","type":{"kind":"OBJECT","name":"EmailList"},"args":[]},
				{"name":"users","type":{"kind":"OBJECT","name":"UserList"},"args":[]}
			]
		}`,
		"Mutation": `{
			"name":"Mutation","kind":"OBJECT",
			"fields":[
				{"name":"syncSmsCampaigns","type":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaignJob"}},"args":[]},
				{"name":"createSmsCampaign","type":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaign"}},"args":[{"name":"input","type":{"kind":"NON_NULL","ofType":{"kind":"INPUT_OBJECT","name":"CreateSmsCampaignInput"}}}]},
				{"name":"duplicateSmsCampaign","type":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaign"}},"args":[{"name":"id","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}}}]},
				{"name":"archiveUser","type":{"kind":"SCALAR","name":"Boolean"},"args":[{"name":"id","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}}}]}
			]
		}`,
		"SmsCampaign": `{
			"name":"SmsCampaign","kind":"OBJECT",
			"fields":[
				{"name":"id","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}},"args":[]},
				{"name":"name","type":{"kind":"SCALAR","name":"String"},"args":[]}
			]
		}`,
		"SmsCampaignList": `{
			"name":"SmsCampaignList","kind":"OBJECT",
			"fields":[
				{"name":"campaigns","type":{"kind":"NON_NULL","ofType":{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaign"}}}},"args":[]},
				{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}
			]
		}`,
		"SmsCampaignJob": `{
			"name":"SmsCampaignJob","kind":"OBJECT",
			"fields":[
				{"name":"campaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"EmailList": `{
			"name":"EmailList","kind":"OBJECT",
			"fields":[
				{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}
			]
		}`,
		"UserList": `{
			"name":"UserList","kind":"OBJECT",
			"fields":[
				{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}
			]
		}`,
		"Account": `{
			"name":"Account","kind":"OBJECT",
			"fields":[
				{"name":"primarySmsCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"CampaignAudit": `{
			"name":"CampaignAudit","kind":"OBJECT",
			"fields":[
				{"name":"smsCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"Client": `{
			"name":"Client","kind":"OBJECT",
			"fields":[
				{"name":"campaigns","type":{"kind":"NON_NULL","ofType":{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"kind":"OBJECT","name":"SmsCampaign"}}}},"args":[]},
				{"name":"id","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"ID"}},"args":[]}
			]
		}`,
		"Dashboard": `{
			"name":"Dashboard","kind":"OBJECT",
			"fields":[
				{"name":"featuredCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"Journey": `{
			"name":"Journey","kind":"OBJECT",
			"fields":[
				{"name":"entryCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"PhoneCallCampaign": `{
			"name":"PhoneCallCampaign","kind":"OBJECT",
			"fields":[
				{"name":"smsCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]},
				{"name":"linkCampaign","type":{"kind":"SCALAR","name":"Boolean"},"args":[{"name":"input","type":{"kind":"INPUT_OBJECT","name":"CreateSmsCampaignInput"}}]}
			]
		}`,
		"Team": `{
			"name":"Team","kind":"OBJECT",
			"fields":[
				{"name":"currentCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"Workspace": `{
			"name":"Workspace","kind":"OBJECT",
			"fields":[
				{"name":"defaultCampaign","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]}
			]
		}`,
		"CreateSmsCampaignInput": `{
			"name":"CreateSmsCampaignInput","kind":"INPUT_OBJECT",
			"inputFields":[
				{"name":"name","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}}}
			]
		}`,
		"CreateSmsProviderInput": `{
			"name":"CreateSmsProviderInput","kind":"INPUT_OBJECT",
			"inputFields":[
				{"name":"providerName","type":{"kind":"SCALAR","name":"String"}},
				{"name":"providerKey","type":{"kind":"SCALAR","name":"String"}},
				{"name":"timeout","type":{"kind":"SCALAR","name":"Int"}}
			]
		}`,
		"CitizenStatus": `{
			"name":"CitizenStatus","kind":"ENUM",
			"enumValues":[{"name":"ACTIVE"},{"name":"INACTIVE"},{"name":"PENDING"}]
		}`,
	}}
	return NewDescriberFromExecFunc(f.exec), f
}

func TestNewDescriberFromExecFunc(t *testing.T) {
	d, fake := newFakeDescriber()

	sdl, err := d.Describe(context.Background(), "Query")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(sdl, "smsCampaigns") {
		t.Errorf("SDL = %q, want it to contain a Query field", sdl)
	}

	// The cache is the reason reaching Describer is worth it at all.
	if _, err := d.Describe(context.Background(), "Query"); err != nil {
		t.Fatalf("second Describe: %v", err)
	}
	if fake.calls["Query"] != 1 {
		t.Errorf("introspected Query %d times, want 1 — results should be cached", fake.calls["Query"])
	}
}

func TestDescribeWithOptions_FiltersFields(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithOptions(context.Background(), "Query", DescribeOptions{
		FieldFilter: "campaign",
		ShowArgs:    true,
	})
	if err != nil {
		t.Fatalf("DescribeWithOptions: %v", err)
	}
	if !strings.Contains(sdl, "smsCampaigns") || !strings.Contains(sdl, "emailCampaigns") {
		t.Errorf("SDL = %q, want both matching fields", sdl)
	}
	if strings.Contains(sdl, "users") {
		t.Errorf("SDL = %q, want the non-matching field dropped", sdl)
	}
	if !strings.Contains(sdl, "limit") {
		t.Errorf("SDL = %q, want ShowArgs to expand argument signatures", sdl)
	}
}

// The gap DescribeWithFieldFilter left: it rebuilds the type from "fields"
// only, so filtering an input type or an enum yielded nothing.
func TestDescribeWithOptions_FiltersInputFieldsAndEnumValues(t *testing.T) {
	d, _ := newFakeDescriber()

	t.Run("input object", func(t *testing.T) {
		sdl, err := d.DescribeWithOptions(context.Background(), "CreateSmsProviderInput", DescribeOptions{FieldFilter: "provider"})
		if err != nil {
			t.Fatalf("DescribeWithOptions: %v", err)
		}
		if !strings.Contains(sdl, "providerName") || !strings.Contains(sdl, "providerKey") {
			t.Errorf("SDL = %q, want the matching input fields", sdl)
		}
		if strings.Contains(sdl, "timeout") {
			t.Errorf("SDL = %q, want the non-matching input field dropped", sdl)
		}
	})

	t.Run("enum", func(t *testing.T) {
		sdl, err := d.DescribeWithOptions(context.Background(), "CitizenStatus", DescribeOptions{FieldFilter: "ACTIVE"})
		if err != nil {
			t.Fatalf("DescribeWithOptions: %v", err)
		}
		if !strings.Contains(sdl, "ACTIVE") || !strings.Contains(sdl, "INACTIVE") {
			t.Errorf("SDL = %q, want the matching enum values", sdl)
		}
		if strings.Contains(sdl, "PENDING") {
			t.Errorf("SDL = %q, want the non-matching enum value dropped", sdl)
		}
	})
}

func TestDescribeWithOptions_EmptyWhenNothingMatches(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithOptions(context.Background(), "Query", DescribeOptions{FieldFilter: "nonexistent"})
	if err != nil {
		t.Fatalf("DescribeWithOptions: %v", err)
	}
	if sdl != "" {
		t.Errorf("SDL = %q, want an empty string so callers can detect no match", sdl)
	}
}

func TestDescribeWithOptions_NoFilterKeepsEverything(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithOptions(context.Background(), "Query", DescribeOptions{})
	if err != nil {
		t.Fatalf("DescribeWithOptions: %v", err)
	}
	for _, field := range []string{"smsCampaigns", "emailCampaigns", "users"} {
		if !strings.Contains(sdl, field) {
			t.Errorf("SDL = %q, want it to contain %q", sdl, field)
		}
	}
}

// Filtering must copy: fetch returns the cached map, and mutating it would
// corrupt every later lookup of that type.
func TestDescribeWithOptions_DoesNotCorruptTheCache(t *testing.T) {
	d, _ := newFakeDescriber()
	ctx := context.Background()

	if _, err := d.DescribeWithOptions(ctx, "Query", DescribeOptions{FieldFilter: "sms"}); err != nil {
		t.Fatalf("filtered describe: %v", err)
	}

	sdl, err := d.Describe(ctx, "Query")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	for _, field := range []string{"smsCampaigns", "emailCampaigns", "users"} {
		if !strings.Contains(sdl, field) {
			t.Errorf("after a filtered describe, SDL = %q, want %q still present", sdl, field)
		}
	}
}

func TestDescribeWithOptions_DepthFollowsSurvivingFields(t *testing.T) {
	d, fake := newFakeDescriber()
	fake.types["SmsCampaignList"] = `{"name":"SmsCampaignList","kind":"OBJECT","fields":[{"name":"campaigns","type":{"kind":"OBJECT","name":"SmsCampaign"},"args":[]},{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}]}`
	fake.types["EmailList"] = `{"name":"EmailList","kind":"OBJECT","fields":[{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}]}`
	fake.types["UserList"] = `{"name":"UserList","kind":"OBJECT","fields":[{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}]}`

	sdl, err := d.DescribeWithOptions(context.Background(), "Query", DescribeOptions{FieldFilter: "smsCampaigns", Depth: 1})
	if err != nil {
		t.Fatalf("DescribeWithOptions: %v", err)
	}
	if !strings.Contains(sdl, "SmsCampaignList") {
		t.Errorf("SDL = %q, want depth to pull in the referenced type", sdl)
	}
	if strings.Contains(sdl, "UserList") {
		t.Errorf("SDL = %q, want recursion to follow only the surviving fields", sdl)
	}
}

func TestDescribeWithDepth_IncludesReferencingOperations(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepthLimits(context.Background(), "SmsCampaign", false, false, 1, 0, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if !strings.Contains(sdl, "# Referenced by top-level operations") {
		t.Fatalf("SDL = %q, want used-by section", sdl)
	}
	if strings.Contains(sdl, "showing ") {
		t.Fatalf("SDL = %q, did not want truncation note for unlimited output", sdl)
	}
	if !strings.Contains(sdl, "smsCampaign(id: ID!): SmsCampaign") {
		t.Errorf("SDL = %q, want direct query reference", sdl)
	}
	if !strings.Contains(sdl, "smsCampaigns(limit: Int): SmsCampaignList!") {
		t.Errorf("SDL = %q, want transitive query reference through SmsCampaignList", sdl)
	}
	if strings.Index(sdl, "smsCampaign(id: ID!): SmsCampaign") > strings.Index(sdl, "smsCampaigns(limit: Int): SmsCampaignList!") {
		t.Errorf("SDL = %q, want direct query return matches ranked ahead of transitive wrapper matches", sdl)
	}
	if !strings.Contains(sdl, "createSmsCampaign(input: CreateSmsCampaignInput!): SmsCampaign!") {
		t.Errorf("SDL = %q, want mutation reference", sdl)
	}
	if strings.Index(sdl, "createSmsCampaign(input: CreateSmsCampaignInput!): SmsCampaign!") > strings.Index(sdl, "syncSmsCampaigns: SmsCampaignJob!") {
		t.Errorf("SDL = %q, want direct mutation return matches ranked ahead of transitive wrapper matches", sdl)
	}
	if strings.Contains(sdl, "archiveUser") {
		t.Errorf("SDL = %q, did not want unrelated operations", sdl)
	}
}

func TestDescribeWithDepth_DoesNotIncludeReferencingOperationsAtDepthZero(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepth(context.Background(), "SmsCampaign", false, false, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepth: %v", err)
	}
	if strings.Contains(sdl, "# Referenced by top-level operations") {
		t.Errorf("SDL = %q, did not want used-by section at depth 0", sdl)
	}
}

func TestDescribeWithDepth_IncludesOperationsReferencingInputTypes(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepthLimits(context.Background(), "CreateSmsCampaignInput", false, false, 1, 0, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if !strings.Contains(sdl, "createSmsCampaign(input: CreateSmsCampaignInput!): SmsCampaign!") {
		t.Errorf("SDL = %q, want mutation that uses the input type as an argument", sdl)
	}
}

func TestDescribeWithDepth_IncludesReferencingFields(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepthLimits(context.Background(), "SmsCampaign", false, false, 1, 0, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if !strings.Contains(sdl, "# Referenced by fields") {
		t.Fatalf("SDL = %q, want referenced-by-fields section", sdl)
	}
	if strings.Contains(sdl, "showing ") {
		t.Fatalf("SDL = %q, did not want truncation note for unlimited output", sdl)
	}
	if !strings.Contains(sdl, "type Client {") || !strings.Contains(sdl, "campaigns: [SmsCampaign!]!") {
		t.Errorf("SDL = %q, want Client.campaigns field reference", sdl)
	}
	if !strings.Contains(sdl, "type PhoneCallCampaign {") || !strings.Contains(sdl, "smsCampaign: SmsCampaign") {
		t.Errorf("SDL = %q, want PhoneCallCampaign.smsCampaign field reference", sdl)
	}
	if strings.Contains(sdl, "id: ID!") && strings.Contains(sdl, `type Client {
  campaigns: [SmsCampaign!]!
  id: ID!`) {
		t.Errorf("SDL = %q, did not want unrelated fields included in referenced-by-fields section", sdl)
	}
}

func TestDescribeWithDepth_IncludesFieldsReferencingInputTypes(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepthLimits(context.Background(), "CreateSmsCampaignInput", false, false, 1, 0, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if !strings.Contains(sdl, "linkCampaign(input: CreateSmsCampaignInput): Boolean") {
		t.Errorf("SDL = %q, want field whose argument references the input type", sdl)
	}
}

func TestDescribeWithDepth_DefaultReverseReferenceCaps(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepth(context.Background(), "SmsCampaign", false, false, 1)
	if err != nil {
		t.Fatalf("DescribeWithDepth: %v", err)
	}
	if !strings.Contains(sdl, "# Referenced by top-level operations (showing 5 of 7)") {
		t.Errorf("SDL = %q, want capped operations header", sdl)
	}
	if !strings.Contains(sdl, "# Referenced by fields (showing 5 of 10)") {
		t.Errorf("SDL = %q, want capped fields header", sdl)
	}
	for _, want := range []string{
		"smsCampaign(id: ID!): SmsCampaign",
		"smsCampaignBySlug(slug: String): SmsCampaign",
		"smsCampaignPreview: SmsCampaign",
		"smsCampaigns(limit: Int): SmsCampaignList!",
		"createSmsCampaign(input: CreateSmsCampaignInput!): SmsCampaign!",
		"type Account {",
		"type CampaignAudit {",
		"type Client {",
		"type Dashboard {",
		"type Journey {",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("SDL = %q, want capped output to include %q", sdl, want)
		}
	}
	for _, unwanted := range []string{
		"duplicateSmsCampaign(id: ID!): SmsCampaign!",
		"syncSmsCampaigns: SmsCampaignJob!",
		"type PhoneCallCampaign {",
		"type Team {",
		"type Workspace {",
	} {
		if strings.Contains(sdl, unwanted) {
			t.Errorf("SDL = %q, did not want capped output to include %q", sdl, unwanted)
		}
	}
}

func TestDescribeWithDepth_ZeroCapsMeanUnlimited(t *testing.T) {
	d, _ := newFakeDescriber()

	sdl, err := d.DescribeWithDepthLimits(context.Background(), "SmsCampaign", false, false, 1, 0, 0)
	if err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if strings.Contains(sdl, "showing ") {
		t.Errorf("SDL = %q, did not want truncation note with unlimited caps", sdl)
	}
	for _, want := range []string{
		"duplicateSmsCampaign(id: ID!): SmsCampaign!",
		"syncSmsCampaigns: SmsCampaignJob!",
		"type PhoneCallCampaign {",
		"type Team {",
		"type Workspace {",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("SDL = %q, want unlimited output to include %q", sdl, want)
		}
	}
}

func TestDescribeWithDepth_ServesTypesFromFullIntrospection(t *testing.T) {
	d, fake := newFakeDescriber()
	if _, err := d.DescribeWithDepthLimits(context.Background(), "SmsCampaign", true, true, 2, 0, 0); err != nil {
		t.Fatalf("DescribeWithDepthLimits: %v", err)
	}
	if fake.calls["__schema"] != 1 {
		t.Errorf("full introspection ran %d times, want 1", fake.calls["__schema"])
	}
	for name, n := range fake.calls {
		if name != "__schema" && n > 0 {
			t.Errorf("introspected %q %d times via __type, want 0 — the full schema already holds it", name, n)
		}
	}
}

func TestTypeInfoFromFullType_MatchesPerTypeQueryShape(t *testing.T) {
	var full map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"name":"Status","kind":"OBJECT","description":"d",
		"fields":[
			{"name":"live","description":"x","isDeprecated":false,"deprecationReason":null,
			 "type":{"kind":"SCALAR","name":"String"},
			 "args":[{"name":"a","description":"arg doc","defaultValue":"1","type":{"kind":"SCALAR","name":"Int"}}]},
			{"name":"old","isDeprecated":true,"type":{"kind":"SCALAR","name":"String"},"args":[]}
		],
		"inputFields":null,
		"enumValues":null,
		"possibleTypes":null
	}`), &full); err != nil {
		t.Fatal(err)
	}
	got := typeInfoFromFullType(full)
	fields, _ := got["fields"].([]interface{})
	if len(fields) != 1 {
		t.Fatalf("fields = %v, want only the non-deprecated one", fields)
	}
	field := fields[0].(map[string]interface{})
	if _, ok := field["isDeprecated"]; ok {
		t.Errorf("field kept isDeprecated: %v", field)
	}
	arg := field["args"].([]interface{})[0].(map[string]interface{})
	if _, ok := arg["description"]; ok {
		t.Errorf("arg kept description, which the per-type query does not fetch: %v", arg)
	}
	if _, ok := arg["defaultValue"]; ok {
		t.Errorf("arg kept defaultValue, which the per-type query does not fetch: %v", arg)
	}
}
