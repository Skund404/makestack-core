// Package schema validates the structural correctness of makestack manifest
// bodies before they are written to disk. It checks JSON field types but does
// not validate semantic content (e.g. whether relationship targets exist).
package schema

import (
	"encoding/json"
	"fmt"
)

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

	// — type-specific checks ——————————————————————————————————————————————————

	switch primType {
	case "technique", "workflow":
		if raw, ok := body["steps"]; ok {
			errs = append(errs, checkArrayField("steps", raw)...)
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

// stringVal extracts the Go string value from a JSON string RawMessage.
// Returns "" if raw is not a valid JSON string.
func stringVal(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
