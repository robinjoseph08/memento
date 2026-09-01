package main

import (
	"fmt"
	"os"

	"github.com/robinjoseph08/web-app-template/internal/templateinit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: %s <go-module-path>", os.Args[0])
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	result, err := templateinit.Initialize(root, os.Args[1])
	if err != nil {
		return err
	}
	fmt.Printf("Initialized %s (%s) in %d files.\n", result.DisplayName, result.Module, result.FilesChanged)
	fmt.Println("Review the changes, run mise setup, and commit the initialized project.")
	return nil
}
