package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseBody is a test helper that parses a JSON object string into the
// map[string]json.RawMessage form expected by Validate.
func parseBody(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &body); err != nil {
		t.Fatalf("parseBody: %v", err)
	}
	return body
}

// hasError checks whether any of the returned errors contain the given substring.
func hasError(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// — Valid bodies ——————————————————————————————————————————————————————————————

func TestValidate_ValidBodies(t *testing.T) {
	cases := []struct {
		name    string
		primType string
		body    string
	}{
		{
			"tool with all optional fields",
			"tool",
			`{"description":"A good tool","tags":["a","b"],"relationships":[{"type":"uses_material","target":"materials/x/manifest.json"}]}`,
		},
		{
			"technique with steps array",
			"technique",
			`{"steps":["step one","step two"]}`,
		},
		{
			"workflow with steps array",
			"workflow",
			`{"steps":[{"action":"cut"}]}`,
		},
		{
			"project with parent_project string",
			"project",
			`{"parent_project":"projects/parent/manifest.json"}`,
		},
		{
			"material with no optional fields",
			"material",
			`{}`,
		},
		{
			"event with no optional fields",
			"event",
			`{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := parseBody(t, tc.body)
			errs := Validate(tc.primType, body)
			if len(errs) != 0 {
				t.Errorf("expected no errors, got: %v", errs)
			}
		})
	}
}

// — description ———————————————————————————————————————————————————————————————

func TestValidate_Description_MustBeString(t *testing.T) {
	body := parseBody(t, `{"description":42}`)
	errs := Validate("tool", body)
	if !hasError(errs, "description") {
		t.Errorf("expected description error, got %v", errs)
	}
}

func TestValidate_Description_StringAccepted(t *testing.T) {
	body := parseBody(t, `{"description":"fine"}`)
	errs := Validate("tool", body)
	if hasError(errs, "description") {
		t.Errorf("unexpected description error: %v", errs)
	}
}

// — tags ——————————————————————————————————————————————————————————————————————

func TestValidate_Tags_MustBeArray(t *testing.T) {
	body := parseBody(t, `{"tags":"not-an-array"}`)
	errs := Validate("tool", body)
	if !hasError(errs, "tags") {
		t.Errorf("expected tags error, got %v", errs)
	}
}

func TestValidate_Tags_ElementMustBeString(t *testing.T) {
	body := parseBody(t, `{"tags":["ok",42,true]}`)
	errs := Validate("tool", body)

	// Expect two element errors (index 1 and 2).
	if !hasError(errs, "tags[1]") {
		t.Errorf("expected tags[1] error, got %v", errs)
	}
	if !hasError(errs, "tags[2]") {
		t.Errorf("expected tags[2] error, got %v", errs)
	}
}

func TestValidate_Tags_EmptyArrayAccepted(t *testing.T) {
	body := parseBody(t, `{"tags":[]}`)
	errs := Validate("tool", body)
	if hasError(errs, "tags") {
		t.Errorf("unexpected tags error for empty array: %v", errs)
	}
}

// — relationships —————————————————————————————————————————————————————————————

func TestValidate_Relationships_MustBeArray(t *testing.T) {
	body := parseBody(t, `{"relationships":"bad"}`)
	errs := Validate("tool", body)
	if !hasError(errs, "relationships") {
		t.Errorf("expected relationships error, got %v", errs)
	}
}

func TestValidate_Relationships_ElementMustBeObject(t *testing.T) {
	body := parseBody(t, `{"relationships":["not-an-object"]}`)
	errs := Validate("tool", body)
	if !hasError(errs, "relationships[0]") {
		t.Errorf("expected relationships[0] error, got %v", errs)
	}
}

func TestValidate_Relationships_MissingType(t *testing.T) {
	body := parseBody(t, `{"relationships":[{"target":"tools/x/manifest.json"}]}`)
	errs := Validate("tool", body)
	if !hasError(errs, `"type"`) {
		t.Errorf("expected missing type error, got %v", errs)
	}
}

func TestValidate_Relationships_MissingTarget(t *testing.T) {
	body := parseBody(t, `{"relationships":[{"type":"uses_tool"}]}`)
	errs := Validate("tool", body)
	if !hasError(errs, `"target"`) {
		t.Errorf("expected missing target error, got %v", errs)
	}
}

func TestValidate_Relationships_EmptyTypeRejected(t *testing.T) {
	body := parseBody(t, `{"relationships":[{"type":"","target":"tools/x/manifest.json"}]}`)
	errs := Validate("tool", body)
	if !hasError(errs, `"type"`) {
		t.Errorf("expected empty type error, got %v", errs)
	}
}

func TestValidate_Relationships_ValidEntry(t *testing.T) {
	body := parseBody(t, `{"relationships":[{"type":"uses_tool","target":"tools/x/manifest.json"}]}`)
	errs := Validate("tool", body)
	if hasError(errs, "relationships") {
		t.Errorf("unexpected relationships error: %v", errs)
	}
}

func TestValidate_Relationships_AllErrorsCollected(t *testing.T) {
	// First entry valid, second missing target, third not an object.
	body := parseBody(t, `{"relationships":[
		{"type":"uses_tool","target":"tools/x/manifest.json"},
		{"type":"uses_material"},
		"bad"
	]}`)
	errs := Validate("tool", body)

	if !hasError(errs, "relationships[1]") {
		t.Errorf("expected relationships[1] error, got %v", errs)
	}
	if !hasError(errs, "relationships[2]") {
		t.Errorf("expected relationships[2] error, got %v", errs)
	}
}

// — type-specific: steps ——————————————————————————————————————————————————————

func TestValidate_Steps_TechniqueMustBeArray(t *testing.T) {
	body := parseBody(t, `{"steps":{"wrong":"shape"}}`)
	errs := Validate("technique", body)
	if !hasError(errs, "steps") {
		t.Errorf("expected steps error for technique, got %v", errs)
	}
}

func TestValidate_Steps_WorkflowMustBeArray(t *testing.T) {
	body := parseBody(t, `{"steps":"bad"}`)
	errs := Validate("workflow", body)
	if !hasError(errs, "steps") {
		t.Errorf("expected steps error for workflow, got %v", errs)
	}
}

func TestValidate_Steps_ToolIgnored(t *testing.T) {
	// tools don't have steps — even a bad steps value should be ignored.
	body := parseBody(t, `{"steps":"ignored"}`)
	errs := Validate("tool", body)
	if hasError(errs, "steps") {
		t.Errorf("unexpected steps error for tool: %v", errs)
	}
}

// — type-specific: parent_project ————————————————————————————————————————————

func TestValidate_ParentProject_MustBeString(t *testing.T) {
	body := parseBody(t, `{"parent_project":123}`)
	errs := Validate("project", body)
	if !hasError(errs, "parent_project") {
		t.Errorf("expected parent_project error, got %v", errs)
	}
}

func TestValidate_ParentProject_StringAccepted(t *testing.T) {
	body := parseBody(t, `{"parent_project":"projects/parent/manifest.json"}`)
	errs := Validate("project", body)
	if hasError(errs, "parent_project") {
		t.Errorf("unexpected parent_project error: %v", errs)
	}
}

func TestValidate_ParentProject_NonProjectIgnored(t *testing.T) {
	// A non-project type should not validate parent_project at all.
	body := parseBody(t, `{"parent_project":999}`)
	errs := Validate("tool", body)
	if hasError(errs, "parent_project") {
		t.Errorf("unexpected parent_project error for tool type: %v", errs)
	}
}

// — multiple errors collected —————————————————————————————————————————————————

func TestValidate_MultipleErrors_AllReturned(t *testing.T) {
	body := parseBody(t, `{
		"description": 42,
		"tags": "bad",
		"relationships": [{"type":""}]
	}`)
	errs := Validate("tool", body)

	// Expect at least three errors.
	if len(errs) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(errs), errs)
	}
}
