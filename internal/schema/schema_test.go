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

// — Core-1: subtype ———————————————————————————————————————————————————————————

func TestValidate_Subtype_ValidValues(t *testing.T) {
	for _, v := range []string{"consumable", "component", "product", "organism"} {
		body := parseBody(t, `{"subtype":"`+v+`"}`)
		errs := Validate("material", body)
		if hasError(errs, "subtype") {
			t.Errorf("subtype %q: unexpected error: %v", v, errs)
		}
	}
}

func TestValidate_Subtype_InvalidValue(t *testing.T) {
	body := parseBody(t, `{"subtype":"gadget"}`)
	errs := Validate("material", body)
	if !hasError(errs, "subtype") {
		t.Errorf("expected subtype error for unknown value, got %v", errs)
	}
}

func TestValidate_Subtype_MustBeString(t *testing.T) {
	body := parseBody(t, `{"subtype":42}`)
	errs := Validate("material", body)
	if !hasError(errs, "subtype") {
		t.Errorf("expected subtype error for non-string, got %v", errs)
	}
}

// — Core-1: occurred_at ———————————————————————————————————————————————————————

func TestValidate_OccurredAt_ValidISO8601(t *testing.T) {
	cases := []string{
		"2026-03-13T10:00:00Z",
		"2026-03-13T10:00:00+05:30",
		"2026-03-13T10:00:00",
		"2026-03-13",
	}
	for _, v := range cases {
		body := parseBody(t, `{"occurred_at":"`+v+`"}`)
		errs := Validate("event", body)
		if hasError(errs, "occurred_at") {
			t.Errorf("occurred_at %q: unexpected error: %v", v, errs)
		}
	}
}

func TestValidate_OccurredAt_InvalidValue(t *testing.T) {
	body := parseBody(t, `{"occurred_at":"not-a-date"}`)
	errs := Validate("event", body)
	if !hasError(errs, "occurred_at") {
		t.Errorf("expected occurred_at error for invalid string, got %v", errs)
	}
}

func TestValidate_OccurredAt_MustBeString(t *testing.T) {
	body := parseBody(t, `{"occurred_at":12345}`)
	errs := Validate("event", body)
	if !hasError(errs, "occurred_at") {
		t.Errorf("expected occurred_at error for non-string, got %v", errs)
	}
}

// — Core-1: status ————————————————————————————————————————————————————————————

func TestValidate_Status_KnownValuesAccepted(t *testing.T) {
	for _, v := range []string{"planned", "active", "complete", "archived"} {
		body := parseBody(t, `{"status":"`+v+`"}`)
		errs := Validate("project", body)
		if hasError(errs, "status") {
			t.Errorf("status %q: unexpected error: %v", v, errs)
		}
	}
}

func TestValidate_Status_UnknownValueAccepted(t *testing.T) {
	// Unknown status values must be accepted (domain packs extend the set).
	body := parseBody(t, `{"status":"in-review"}`)
	errs := Validate("project", body)
	if hasError(errs, "status") {
		t.Errorf("status unknown value should be accepted, got %v", errs)
	}
}

func TestValidate_Status_MustBeString(t *testing.T) {
	body := parseBody(t, `{"status":42}`)
	errs := Validate("project", body)
	if !hasError(errs, "status") {
		t.Errorf("expected status error for non-string, got %v", errs)
	}
}

// — Core-2: typed Step schema —————————————————————————————————————————————————

func TestValidate_Steps_PlainStrings_AlwaysValid(t *testing.T) {
	body := parseBody(t, `{"steps":["cut the leather","punch holes","stitch"]}`)
	errs := Validate("technique", body)
	if len(errs) != 0 {
		t.Errorf("plain string steps: expected no errors, got %v", errs)
	}
}

func TestValidate_Steps_UnstructuredObjects_Skipped(t *testing.T) {
	// Objects without "order" are treated as unstructured — no Step validation.
	body := parseBody(t, `{"steps":[{"action":"cut","tool":"knife"},{"action":"punch"}]}`)
	errs := Validate("technique", body)
	if len(errs) != 0 {
		t.Errorf("unstructured step objects: expected no errors, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_AllFieldsValid(t *testing.T) {
	body := parseBody(t, `{"steps":[{
		"order": 1,
		"title": "Cut hide",
		"notes": "Use a sharp knife",
		"technique_ref": "techniques/straight-cut/manifest.json",
		"duration": {"value": 10, "unit": "min"},
		"parameters": {"thickness": {"value": 3.5, "unit": "mm"}},
		"requirements": [
			{"type": "uses_tool", "target": "tools/knife/manifest.json", "quantity": 1, "unit": "ea"}
		]
	}]}`)
	errs := Validate("technique", body)
	if len(errs) != 0 {
		t.Errorf("fully valid typed step: expected no errors, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_MissingOrder(t *testing.T) {
	// "order" field present but not an integer — triggers typed step validation.
	body := parseBody(t, `{"steps":[{"order":"first","title":"Cut"}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, `"order"`) {
		t.Errorf("expected order error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_MissingTitle(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, `"title"`) {
		t.Errorf("expected title error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_EmptyTitle(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":""}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, `"title"`) {
		t.Errorf("expected title error for empty string, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_InvalidDuration(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","duration":"10min"}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, "duration") {
		t.Errorf("expected duration error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_DurationMissingUnit(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","duration":{"value":10}}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, "duration") {
		t.Errorf("expected duration unit error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_DurationMissingValue(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","duration":{"unit":"min"}}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, "duration") {
		t.Errorf("expected duration value error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_InvalidTechniqueRef(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","technique_ref":"saddle-stitching"}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, "technique_ref") {
		t.Errorf("expected technique_ref error for non-path string, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_ValidTechniqueRef(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Stitch","technique_ref":"techniques/saddle-stitching/manifest.json"}]}`)
	errs := Validate("technique", body)
	if hasError(errs, "technique_ref") {
		t.Errorf("unexpected technique_ref error: %v", errs)
	}
}

func TestValidate_Steps_TypedStep_InvalidParameters(t *testing.T) {
	// Parameter value is a bare number instead of a measurement object.
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","parameters":{"depth":3.5}}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, "parameters") {
		t.Errorf("expected parameters error for bare number, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_ValidParameters(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","parameters":{"depth":{"value":3.5,"unit":"mm"}}}]}`)
	errs := Validate("technique", body)
	if hasError(errs, "parameters") {
		t.Errorf("unexpected parameters error: %v", errs)
	}
}

func TestValidate_Steps_TypedStep_RequirementsMissingTarget(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","requirements":[{"type":"uses_tool"}]}]}`)
	errs := Validate("technique", body)
	if !hasError(errs, `"target"`) {
		t.Errorf("expected requirements target error, got %v", errs)
	}
}

func TestValidate_Steps_TypedStep_RequirementsValid(t *testing.T) {
	body := parseBody(t, `{"steps":[{"order":1,"title":"Cut","requirements":[
		{"type":"uses_tool","target":"tools/knife/manifest.json"}
	]}]}`)
	errs := Validate("technique", body)
	if hasError(errs, "requirements") {
		t.Errorf("unexpected requirements error: %v", errs)
	}
}

func TestValidate_Steps_MixedArray(t *testing.T) {
	// Mixed: plain string, unstructured object, valid typed step, invalid typed step.
	body := parseBody(t, `{"steps":[
		"plain string step",
		{"action": "unstructured, no order field"},
		{"order": 3, "title": "Valid typed step"},
		{"order": 4}
	]}`)
	errs := Validate("technique", body)

	// Only steps[3] (missing title) should produce an error.
	if len(errs) != 1 {
		t.Errorf("expected exactly 1 error (steps[3] missing title), got %d: %v", len(errs), errs)
	}
	if !hasError(errs, "steps[3]") {
		t.Errorf("expected steps[3] error, got %v", errs)
	}
}

func TestValidate_Steps_WorkflowTypedSteps(t *testing.T) {
	// Typed step validation works for workflow type too.
	body := parseBody(t, `{"steps":[{"order":1}]}`)
	errs := Validate("workflow", body)
	if !hasError(errs, `"title"`) {
		t.Errorf("workflow: expected title error, got %v", errs)
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
