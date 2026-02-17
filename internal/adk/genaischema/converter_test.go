package genaischema_test

import (
	"testing"

	"github.com/metalagman/norma/internal/adk/genaischema"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

func TestFromJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		validate func(*testing.T, *genai.Schema)
	}{
		{
			name: "full_features",
			input: `{
				"title": "My Object",
				"type": "object",
				"description": "a complex object",
				"properties": {
					"name": { 
						"type": "string", 
						"minLength": 1, 
						"maxLength": 100,
						"pattern": "^[a-z]+$",
						"default": "guest"
					},
					"age": { 
						"type": "integer", 
						"minimum": 0, 
						"maximum": 150 
					},
					"scores": {
						"type": "array",
						"minItems": 1,
						"maxItems": 5,
						"items": { "type": "number" }
					}
				},
				"required": ["name"],
				"propertyOrdering": ["name", "age", "scores"],
				"nullable": true
			}`,
			validate: func(t *testing.T, s *genai.Schema) {
				assert.Equal(t, "My Object", s.Title)
				assert.Equal(t, genai.TypeObject, s.Type)
				assert.Equal(t, "a complex object", s.Description)
				assert.True(t, *s.Nullable)
				assert.Equal(t, []string{"name", "age", "scores"}, s.PropertyOrdering)

				name := s.Properties["name"]
				assert.Equal(t, int64(1), *name.MinLength)
				assert.Equal(t, int64(100), *name.MaxLength)
				assert.Equal(t, "^[a-z]+$", name.Pattern)
				assert.Equal(t, "guest", name.Default)

				age := s.Properties["age"]
				assert.Equal(t, 0.0, *age.Minimum)
				assert.Equal(t, 150.0, *age.Maximum)

				scores := s.Properties["scores"]
				assert.Equal(t, int64(1), *scores.MinItems)
				assert.Equal(t, int64(5), *scores.MaxItems)
			},
		},
		{
			name: "anyOf",
			input: `{
				"anyOf": [
					{ "type": "string" },
					{ "type": "integer" }
				]
			}`,
			validate: func(t *testing.T, s *genai.Schema) {
				assert.Len(t, s.AnyOf, 2)
				assert.Equal(t, genai.TypeString, s.AnyOf[0].Type)
				assert.Equal(t, genai.TypeInteger, s.AnyOf[1].Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := genaischema.FromJSON([]byte(tt.input))
			assert.NoError(t, err)
			tt.validate(t, s)
		})
	}
}
