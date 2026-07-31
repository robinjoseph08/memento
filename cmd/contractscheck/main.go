package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/robinjoseph08/memento/internal/contractscheck"
)

const existingViolationsPath = "scripts/contracts/go-existing-violations.txt"

func main() {
	diagnostics, err := contractscheck.CheckGo(".", "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	baseline, err := readBaseline(existingViolationsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	observed := make(map[string]bool, len(diagnostics))
	var unexpected []string
	for _, diagnostic := range diagnostics {
		key := diagnosticKey(diagnostic)
		observed[key] = true
		if !baseline[key] {
			unexpected = append(unexpected, diagnostic)
		}
	}
	var stale []string
	for entry := range baseline {
		if !observed[entry] {
			stale = append(stale, "stale Go contract baseline entry: "+entry)
		}
	}
	sort.Strings(stale)
	unexpected = append(unexpected, stale...)
	if len(unexpected) == 0 {
		return
	}
	for _, diagnostic := range unexpected {
		fmt.Fprintln(os.Stderr, diagnostic)
	}
	os.Exit(1)
}

func readBaseline(filename string) (map[string]bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("read Go contract baseline: %w", err)
	}
	defer file.Close()
	result := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result[line] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Go contract baseline: %w", err)
	}
	return result, nil
}

func diagnosticKey(diagnostic string) string {
	parts := strings.SplitN(diagnostic, ":", 4)
	if len(parts) != 4 {
		return diagnostic
	}
	return parts[0] + ":" + parts[3]
}
