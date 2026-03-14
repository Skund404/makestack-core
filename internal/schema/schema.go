// Package schema validates the structural correctness of makestack manifest
// bodies before they are written to disk. It checks JSON field types but does
// not validate semantic content (e.g. whether relationship targets exist).
package schema

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// validSubtypes is the closed set of accepted material subtype values.
var validSubtypes = map[string]bool{
	"consumable": true,
	"component":  true,
	"product":    true,
	"organism":   true,
}

// Validate checks body against structural rules for the given primitive type.
// All common checks are run first; type-specific checks follow.
// Returns a slice of human-readable error strings; an empty slice means valid.
func Validate(primType string, body map[string]json.RawMessage) []string {
	var errs []string

	// — common checks (all primitive types) ——————————————————————————————————

	if raw, ok := body["description"]; ok {
		if !isString(raw) {
			errs = append(errs, "description: must be a string")
		}
	}

	if raw, ok := body["tags"]; ok {
		errs = append(errs, checkTagsField(raw)...)
	}

	if raw, ok := body["relationships"]; ok {
		errs = append(errs, checkRelationshipsField(raw)...)
	}

	// Primitives Evolution fields (Core-1): validate when present.
	if raw, ok := body["subtype"]; ok {
		if !isString(raw) {
			errs = append(errs, "subtype: must be a string")
		} else if s := stringVal(raw); !validSubtypes[s] {
			errs = append(errs, fmt.Sprintf(
				"subtype: must be one of consumable, component, product, organism; got %q", s))
		}
	}

	if raw, ok := body["occurred_at"]; ok {
		if !isISO8601(raw) {
			errs = append(errs, "occurred_at: must be a valid ISO8601 date or date-time string")
		}
	}

	if raw, ok := body["status"]; ok {
		// Warn (string type only) — domain packs may extend the value set,
		// so unknown values are accepted without error.
		if !isString(raw) {
			errs = append(errs, "status: must be a string")
		}
	}

	// — type-specific checks ——————————————————————————————————————————————————

	switch primType {
	case "technique", "workflow":
		if raw, ok := body["steps"]; ok {
			errs = append(errs, checkStepsField(raw)...)
		}

	case "project":
		if raw, ok := body["parent_project"]; ok {
			if !isString(raw) {
				errs = append(errs, "parent_project: must be a string")
			}
		}
	}

	return errs
}

// — helpers ————————————————————————————————————————————————————————————————————

// isString reports whether raw is a JSON string token.
func isString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil
}

// checkTagsField validates that raw is a JSON array of strings.
// Returns error strings prefixed with "tags".
func checkTagsField(raw json.RawMessage) []string {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return []string{"tags: must be an array of strings"}
	}
	var errs []string
	for i, elem := range elems {
		if !isString(elem) {
			errs = append(errs, fmt.Sprintf("tags[%d]: element must be a string", i))
		}
	}
	return errs
}

// checkRelationshipsField validates that raw is a JSON array of relationship
// objects, each with a non-empty string "type" and non-empty string "target".
func checkRelationshipsField(raw json.RawMessage) []string {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return []string{"relationships: must be an array"}
	}

	var errs []string
	for i, elem := range elems {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(elem, &obj); err != nil {
			errs = append(errs, fmt.Sprintf("relationships[%d]: must be an object", i))
			continue
		}

		typeRaw, hasType := obj["type"]
		if !hasType || !isString(typeRaw) || stringVal(typeRaw) == "" {
			errs = append(errs, fmt.Sprintf(`relationships[%d]: missing required field "type"`, i))
		}

		targetRaw, hasTarget := obj["target"]
		if !hasTarget || !isString(targetRaw) || stringVal(targetRaw) == "" {
			errs = append(errs, fmt.Sprintf(`relationships[%d]: missing required field "target"`, i))
		}
	}
	return errs
}

// checkArrayField validates that raw is a JSON array. It does not inspect
// element types — callers use this for loosely-typed arrays like `steps`.
func checkArrayField(name string, raw json.RawMessage) []string {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return []string{name + ": must be an array"}
	}
	return nil
}

// checkStepsField validates the steps array. Each element is checked
// independently:
//   - Plain strings are always valid (backward compat).
//   - Objects without an "order" field are treated as unstructured and skipped.
//   - Objects with an "order" field are validated as typed Steps.
func checkStepsField(raw json.RawMessage) []string {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return []string{"steps: must be an array"}
	}

	var errs []string
	for i, elem := range elems {
		prefix := fmt.Sprintf("steps[%d]", i)

		// Plain strings are always valid.
		if isString(elem) {
			continue
		}

		var obj map[string]json.RawMessage
		if err := json.Unmarshal(elem, &obj); err != nil {
			errs = append(errs, prefix+": must be a string or an object")
			continue
		}

		// Only validate as a typed Step when "order" is present.
		if _, hasOrder := obj["order"]; !hasOrder {
			continue
		}

		errs = append(errs, checkTypedStep(prefix, obj)...)
	}
	return errs
}

// checkTypedStep validates a step object that has been identified as a typed
// Step (i.e. it contains an "order" field). Errors are prefixed with prefix.
func checkTypedStep(prefix string, obj map[string]json.RawMessage) []string {
	var errs []string

	// order: integer, required.
	orderRaw := obj["order"] // already confirmed present
	if !isInteger(orderRaw) {
		errs = append(errs, prefix+`: "order" must be an integer`)
	}

	// title: string, required.
	titleRaw, hasTitle := obj["title"]
	if !hasTitle || !isString(titleRaw) || stringVal(titleRaw) == "" {
		errs = append(errs, prefix+`: missing required field "title"`)
	}

	// notes: string, optional.
	if notesRaw, ok := obj["notes"]; ok {
		if !isString(notesRaw) {
			errs = append(errs, prefix+`: "notes" must be a string`)
		}
	}

	// technique_ref: string, optional; must look like a catalogue path.
	if refRaw, ok := obj["technique_ref"]; ok {
		if !isString(refRaw) {
			errs = append(errs, prefix+`: "technique_ref" must be a string`)
		} else if s := stringVal(refRaw); !looksLikeCataloguePath(s) {
			errs = append(errs, prefix+`: "technique_ref" must be a catalogue path ending with /manifest.json`)
		}
	}

	// duration: {value: number, unit: string}, optional.
	if durRaw, ok := obj["duration"]; ok {
		for _, e := range checkMeasurement(durRaw) {
			errs = append(errs, prefix+`: "duration" `+e)
		}
	}

	// parameters: map of key → {value: number, unit: string}, optional.
	if paramsRaw, ok := obj["parameters"]; ok {
		errs = append(errs, checkParameters(prefix, paramsRaw)...)
	}

	// requirements: array of requirement objects, optional.
	if reqsRaw, ok := obj["requirements"]; ok {
		errs = append(errs, checkRequirements(prefix, reqsRaw)...)
	}

	return errs
}

// checkMeasurement validates a {value: number, unit: string} object.
// Returns error fragments (without prefix) so callers can compose them.
func checkMeasurement(raw json.RawMessage) []string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return []string{`must be an object with "value" (number) and "unit" (string)`}
	}
	var errs []string
	valRaw, hasVal := obj["value"]
	if !hasVal || !isNumber(valRaw) {
		errs = append(errs, `"value" must be a number`)
	}
	unitRaw, hasUnit := obj["unit"]
	if !hasUnit || !isString(unitRaw) {
		errs = append(errs, `"unit" must be a string`)
	}
	return errs
}

// checkParameters validates a parameters map: each value must be a
// {value: number, unit: string} measurement object.
func checkParameters(stepPrefix string, raw json.RawMessage) []string {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return []string{stepPrefix + `: "parameters" must be an object`}
	}
	var errs []string
	for key, val := range params {
		for _, e := range checkMeasurement(val) {
			errs = append(errs, fmt.Sprintf(`%s: "parameters".%s: %s`, stepPrefix, key, e))
		}
	}
	return errs
}

// checkRequirements validates a requirements array. Each element must be an
// object with non-empty string "type" and "target". Other fields are optional.
func checkRequirements(stepPrefix string, raw json.RawMessage) []string {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return []string{stepPrefix + `: "requirements" must be an array`}
	}
	var errs []string
	for i, elem := range elems {
		rp := fmt.Sprintf(`%s: "requirements"[%d]`, stepPrefix, i)
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(elem, &obj); err != nil {
			errs = append(errs, rp+": must be an object")
			continue
		}
		typeRaw, hasType := obj["type"]
		if !hasType || !isString(typeRaw) || stringVal(typeRaw) == "" {
			errs = append(errs, rp+`: missing required field "type"`)
		}
		targetRaw, hasTarget := obj["target"]
		if !hasTarget || !isString(targetRaw) || stringVal(targetRaw) == "" {
			errs = append(errs, rp+`: missing required field "target"`)
		}
	}
	return errs
}

// isNumber reports whether raw is a JSON number token.
func isNumber(raw json.RawMessage) bool {
	var n float64
	return json.Unmarshal(raw, &n) == nil
}

// isInteger reports whether raw is a JSON number with no fractional part.
func isInteger(raw json.RawMessage) bool {
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return false
	}
	return n == float64(int64(n))
}

// looksLikeCataloguePath reports whether s has the form of a catalogue path
// (a non-empty string ending with /manifest.json).
func looksLikeCataloguePath(s string) bool {
	return s != "" && strings.HasSuffix(s, "/manifest.json")
}

// stringVal extracts the Go string value from a JSON string RawMessage.
// Returns "" if raw is not a valid JSON string.
func stringVal(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// isISO8601 reports whether raw is a JSON string containing a valid ISO8601
// date or date-time. Accepts RFC3339 (with timezone), date-time without
// timezone, and date-only forms.
func isISO8601(raw json.RawMessage) bool {
	s := stringVal(raw)
	if s == "" {
		return false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}
