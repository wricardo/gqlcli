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

func newFakeDescriber() (*Describer, *fakeIntrospector) {
	f := &fakeIntrospector{calls: map[string]int{}, types: map[string]string{
		"Query": `{
			"name":"Query","kind":"OBJECT",
			"fields":[
				{"name":"smsCampaigns","type":{"kind":"OBJECT","name":"SmsCampaignList"},"args":[{"name":"limit","type":{"kind":"SCALAR","name":"Int"}}]},
				{"name":"emailCampaigns","type":{"kind":"OBJECT","name":"EmailList"},"args":[]},
				{"name":"users","type":{"kind":"OBJECT","name":"UserList"},"args":[]}
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
	fake.types["SmsCampaignList"] = `{"name":"SmsCampaignList","kind":"OBJECT","fields":[{"name":"total","type":{"kind":"SCALAR","name":"Int"},"args":[]}]}`
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
