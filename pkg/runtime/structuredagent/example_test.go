package structuredagent

import (
	"fmt"
	"iter"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func ExampleNewAgent() {
	inner, err := adkagent.New(adkagent.Config{
		Name:        "formatter",
		Description: "Formats structured output",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx.InvocationID())
				ev.Content = genai.NewContentFromText(`{"output":"done"}`, genai.RoleModel)
				ev.TurnComplete = true
				_ = yield(ev, nil)
			}
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	wrapped, err := NewAgent(inner)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(wrapped.Name())
	// Output:
	// formatter_structured
}
