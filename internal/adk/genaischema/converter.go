package genaischema

import (
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// FromJSON converts a JSON schema byte slice to a genai.Schema.
func FromJSON(data []byte) (*genai.Schema, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal json schema: %w", err)
	}
	return FromMap(raw)
}

// FromMap converts a map representing a JSON schema to a genai.Schema.
func FromMap(m map[string]any) (*genai.Schema, error) {
	s := &genai.Schema{}

	if t, ok := m["type"].(string); ok {
		switch t {
		case "string":
			s.Type = genai.TypeString
		case "number":
			s.Type = genai.TypeNumber
		case "integer":
			s.Type = genai.TypeInteger
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		case "object":
			s.Type = genai.TypeObject
		case "null":
			s.Type = genai.TypeNULL
		default:
			s.Type = genai.TypeUnspecified
		}
	}

	if title, ok := m["title"].(string); ok {
		s.Title = title
	}

	if desc, ok := m["description"].(string); ok {
		s.Description = desc
	}

	if format, ok := m["format"].(string); ok {
		s.Format = format
	}

	if pattern, ok := m["pattern"].(string); ok {
		s.Pattern = pattern
	}

	if def, ok := m["default"]; ok {
		s.Default = def
	}

	if ex, ok := m["example"]; ok {
		s.Example = ex
	}

	if nullable, ok := m["nullable"].(bool); ok {
		s.Nullable = &nullable
	}

	// Constraints
	s.MaxLength = asInt64Ptr(m["maxLength"])
	s.MinLength = asInt64Ptr(m["minLength"])
	s.MaxItems = asInt64Ptr(m["maxItems"])
	s.MinItems = asInt64Ptr(m["minItems"])
	s.MaxProperties = asInt64Ptr(m["maxProperties"])
	s.MinProperties = asInt64Ptr(m["minProperties"])
	s.Maximum = asFloat64Ptr(m["maximum"])
	s.Minimum = asFloat64Ptr(m["minimum"])

	if enum, ok := m["enum"].([]any); ok {
		s.Enum = make([]string, 0, len(enum))
		for _, v := range enum {
			if str, ok := v.(string); ok {
				s.Enum = append(s.Enum, str)
			}
		}
	}

	if anyOf, ok := m["anyOf"].([]any); ok {
		s.AnyOf = make([]*genai.Schema, 0, len(anyOf))
		for _, v := range anyOf {
			if subMap, ok := v.(map[string]any); ok {
				subSchema, err := FromMap(subMap)
				if err != nil {
					return nil, fmt.Errorf("anyOf: %w", err)
				}
				s.AnyOf = append(s.AnyOf, subSchema)
			}
		}
	}

	if props, ok := m["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema)
		for k, v := range props {
			if propMap, ok := v.(map[string]any); ok {
				propSchema, err := FromMap(propMap)
				if err != nil {
					return nil, fmt.Errorf("property %q: %w", k, err)
				}
				s.Properties[k] = propSchema
			}
		}
	}

	if order, ok := m["propertyOrdering"].([]any); ok {
		s.PropertyOrdering = make([]string, 0, len(order))
		for _, v := range order {
			if str, ok := v.(string); ok {
				s.PropertyOrdering = append(s.PropertyOrdering, str)
			}
		}
	}

	if items, ok := m["items"].(map[string]any); ok {
		itemSchema, err := FromMap(items)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		s.Items = itemSchema
	}

	if req, ok := m["required"].([]any); ok {
		s.Required = make([]string, 0, len(req))
		for _, v := range req {
			if str, ok := v.(string); ok {
				s.Required = append(s.Required, str)
			}
		}
	}

	return s, nil
}

func asInt64Ptr(v any) *int64 {
	if v == nil {
		return nil
	}
	var val int64
	switch t := v.(type) {
	case float64:
		val = int64(t)
	case int:
		val = int64(t)
	case int64:
		val = t
	default:
		return nil
	}
	return &val
}

func asFloat64Ptr(v any) *float64 {
	if v == nil {
		return nil
	}
	var val float64
	switch t := v.(type) {
	case float64:
		val = t
	case int:
		val = float64(t)
	case int64:
		val = float64(t)
	default:
		return nil
	}
	return &val
}
