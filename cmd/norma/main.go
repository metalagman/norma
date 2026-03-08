package main

import (
	"errors"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}
