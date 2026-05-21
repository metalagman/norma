package appconfig

import "fmt"

func ExampleValidateSettings() {
	settings := map[string]any{
		"providers": map[string]any{
			"openai": map[string]any{
				"type": "openai",
				"openai": map[string]any{
					"api_key": "test-key",
					"model":   "gpt-5",
				},
			},
		},
	}

	fmt.Println(ValidateSettings(settings) == nil)
	// Output:
	// true
}
