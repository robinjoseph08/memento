package main

import (
	"fmt"
	"os"

	"github.com/robinjoseph08/memento/internal/contractscheck"
)

func main() {
	diagnostics, err := contractscheck.CheckGo(".", "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(diagnostics) == 0 {
		return
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(os.Stderr, diagnostic)
	}
	os.Exit(1)
}
