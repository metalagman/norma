package agentconfig

import "fmt"

func ExampleNormalizeConfig() {
	resolved, err := NormalizeConfig(Config{
		Type: AgentTypeOpenAI,
		OpenAI: &LocalAPIConfig{
			APIKey: "test-key",
			Model:  "gpt-5",
		},
	}, "")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(resolved.Type)
	fmt.Println(resolved.Model)
	// Output:
	// openai
	// gpt-5
}
