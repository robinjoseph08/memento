package devtool

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const maxProjectIdentifierLength = 40

func ResolveEnvironment(ctx context.Context) (Environment, error) {
	currentRoot, err := gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return Environment{}, err
	}
	commonDir, err := gitOutput(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Environment{}, err
	}
	mainRoot := filepath.Dir(commonDir)
	mainRoot, err = filepath.EvalSymlinks(mainRoot)
	if err != nil {
		return Environment{}, fmt.Errorf("resolve main worktree path: %w", err)
	}
	currentRoot, err = filepath.EvalSymlinks(currentRoot)
	if err != nil {
		return Environment{}, fmt.Errorf("resolve current worktree path: %w", err)
	}

	port, err := readPostgresPort(mainRoot)
	if err != nil {
		return Environment{}, err
	}
	return Environment{
		MainRoot:        mainRoot,
		CurrentRoot:     currentRoot,
		MainDatabase:    repositoryIdentifier(mainRoot),
		CurrentDatabase: worktreeDatabaseName(mainRoot, currentRoot),
		PostgresPort:    port,
		ProjectName:     projectName(mainRoot),
	}, nil
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func worktreeDatabaseName(mainRoot, currentRoot string) string {
	project := repositoryIdentifier(mainRoot)
	if samePath(mainRoot, currentRoot) {
		return project
	}
	hash := shortHash(currentRoot)
	slug := sanitizeIdentifier(filepath.Base(currentRoot))
	const suffixLength = 1 + 8
	const maxIdentifierLength = 63
	maxSlugLength := maxIdentifierLength - len(project) - 1 - suffixLength
	if len(slug) > maxSlugLength {
		slug = slug[:maxSlugLength]
	}
	return project + "_" + slug + "_" + hash
}

func projectName(mainRoot string) string {
	return repositoryIdentifier(mainRoot) + "_" + shortHash(mainRoot)
}

func repositoryIdentifier(root string) string {
	identifier := sanitizeIdentifier(filepath.Base(root))
	if len(identifier) > maxProjectIdentifierLength {
		identifier = identifier[:maxProjectIdentifierLength]
	}
	return identifier
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}

func sanitizeIdentifier(value string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, character := range strings.ToLower(value) {
		valid := unicode.IsLetter(character) || unicode.IsDigit(character)
		if valid && character <= unicode.MaxASCII {
			builder.WriteRune(character)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && builder.Len() > 0 {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "worktree"
	}
	return result
}

func postgresEnvironmentPath(mainRoot string) string {
	return filepath.Join(mainRoot, "tmp", "database.env")
}

func readPostgresPort(mainRoot string) (int, error) {
	file, err := os.Open(postgresEnvironmentPath(mainRoot))
	if os.IsNotExist(err) {
		return 5432, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open PostgreSQL environment: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found && key == "POSTGRES_PORT" {
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return 0, fmt.Errorf("invalid POSTGRES_PORT in %s", file.Name())
			}
			return port, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read PostgreSQL environment: %w", err)
	}
	return 0, fmt.Errorf("POSTGRES_PORT is missing from %s", file.Name())
}
