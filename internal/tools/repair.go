// repair.go - conservative, schema-guided repair of tool-call arguments.
//
// Small models emit almost-right arguments: "42" as a string where an
// integer goes, "true" as a string, a scalar where an array goes, a
// JSON blob inside a string where an object goes. RepairArgs applies
// only unambiguous fixes and reports each one; anything doubtful is
// left untouched, so the tool's own validation errors back to the
// model exactly as before (stage 14 of docs/ROADMAP.md).
package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RepairArgs coerces the tool-call arguments against the tool's JSON
// Schema. It returns the (possibly unchanged) arguments and the list of
// applied repairs, one per coerced property. A nil repairs slice means
// the arguments went through untouched.
func RepairArgs(schema, args json.RawMessage) (json.RawMessage, []string, error) {
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		// Not a JSON object: nothing conservative to do. The tool's own
		// decoding produces the error the model needs to see.
		return args, nil, nil
	}
	var sch struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &sch); err != nil {
		return args, nil, fmt.Errorf("repair: unreadable schema: %w", err)
	}
	var repairs []string
	changed := false
	for name, val := range obj {
		prop, ok := sch.Properties[name]
		if !ok {
			continue
		}
		switch prop.Type {
		case "integer":
			if s, isStr := val.(string); isStr {
				if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
					obj[name] = n
					repairs = append(repairs, fmt.Sprintf("%s: string %q -> integer %d", name, s, n))
					changed = true
				}
			}
		case "number":
			if s, isStr := val.(string); isStr {
				if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
					obj[name] = f
					repairs = append(repairs, fmt.Sprintf("%s: string %q -> number %v", name, s, f))
					changed = true
				}
			}
		case "boolean":
			if s, isStr := val.(string); isStr {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "true":
					obj[name] = true
				case "false":
					obj[name] = false
				default:
					continue
				}
				repairs = append(repairs, fmt.Sprintf("%s: string %q -> boolean", name, s))
				changed = true
			}
		case "array":
			if _, isArr := val.([]any); !isArr {
				obj[name] = []any{val}
				repairs = append(repairs, fmt.Sprintf("%s: scalar wrapped into a 1-element array", name))
				changed = true
			}
		case "object":
			if s, isStr := val.(string); isStr {
				var inner map[string]any
				if err := json.Unmarshal([]byte(s), &inner); err == nil && inner != nil {
					obj[name] = inner
					repairs = append(repairs, fmt.Sprintf("%s: JSON string unpacked into an object", name))
					changed = true
				}
			}
		}
	}
	if !changed {
		return args, nil, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return args, nil, fmt.Errorf("repair: re-encode: %w", err)
	}
	return out, repairs, nil
}
