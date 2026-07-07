package hostedagent

import (
	"context"
	"fmt"
	"iter"

	"google.golang.org/adk/v2/model"
)

type exampleModel struct{}

func (exampleModel) Name() string {
	return "example-model"
}

func (exampleModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func ExampleNew() {
	agent, err := New(Config{
		Name:        "assistant",
		Description: "Hosted assistant",
		Model:       exampleModel{},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(agent.Name())
	fmt.Println(agent.Description())
	// Output:
	// assistant
	// Hosted assistant
}
