package sessionstate

import "fmt"

func ExampleCWDKey() {
	state := map[string]any{
		CWDKey: "/workspace",
	}

	fmt.Println(state[CWDKey])
	// Output:
	// /workspace
}
