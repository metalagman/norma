package main

import (
	"os"

	"github.com/normahq/norma/cmd/codex-acp/cmd"
)

func main() {
	if err := command.Command().Execute(); err != nil {
		os.Exit(1)
	}
}
