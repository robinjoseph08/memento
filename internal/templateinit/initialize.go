package templateinit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/mod/module"
)

var (
	projectSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	replacements       = []struct {
		marker func() string
		value  func(project) string
	}{
		{marker: func() string { return "github.com/robinjoseph08/" + "web-app-template" }, value: func(p project) string { return p.module }},
		{marker: func() string { return "web-app" + "-template" }, value: func(p project) string { return p.slug }},
		{marker: func() string { return "web_app_" + "template" }, value: func(p project) string { return p.database }},
		{marker: func() string { return "Web App" + " Template" }, value: func(p project) string { return p.displayName }},
		{marker: func() string { return "A full-stack web application " + "template" }, value: func(p project) string { return p.displayName + " is a full-stack web application" }},
	}
)

type project struct {
	module      string
	slug        string
	database    string
	displayName string
}

type fileChange struct {
	path     string
	contents []byte
	mode     os.FileMode
	remove   bool
	original []byte
}

// Result describes the repository changes made by Initialize.
type Result struct {
	DisplayName  string
	FilesChanged int
	Module       string
	Slug         string
}

// Initialize replaces the template identity in every tracked text file and
// removes files that only support template generation.
func Initialize(root, modulePath string) (Result, error) {
	project, err := newProject(modulePath)
	if err != nil {
		return Result{}, err
	}
	files, err := trackedFiles(root)
	if err != nil {
		return Result{}, err
	}
	changes, markerFound, err := planChanges(root, files, project)
	if err != nil {
		return Result{}, err
	}
	if !markerFound {
		return Result{}, errors.New("template markers were not found; this repository may already be initialized")
	}
	if err := applyChanges(changes); err != nil {
		return Result{}, err
	}
	return Result{
		DisplayName:  project.displayName,
		FilesChanged: len(changes),
		Module:       project.module,
		Slug:         project.slug,
	}, nil
}

func planChanges(root string, files []string, project project) ([]fileChange, bool, error) {
	changes := make([]fileChange, 0)
	markerFound := false
	for _, relative := range files {
		filePath := filepath.Join(root, filepath.FromSlash(relative))
		contents, err := os.ReadFile(filePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", relative, err)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			return nil, false, fmt.Errorf("stat %s: %w", relative, err)
		}
		if isTemplateOnlyFile(relative) {
			changes = append(changes, fileChange{path: filePath, mode: info.Mode().Perm(), original: contents, remove: true})
			continue
		}
		if bytes.IndexByte(contents, 0) >= 0 {
			continue
		}

		updated, err := stripTemplateOnlySections(string(contents))
		if err != nil {
			return nil, false, fmt.Errorf("initialize %s: %w", relative, err)
		}
		for _, replacement := range replacements {
			marker := replacement.marker()
			if strings.Contains(updated, marker) {
				markerFound = true
				updated = strings.ReplaceAll(updated, marker, replacement.value(project))
			}
		}
		if updated != string(contents) {
			changes = append(changes, fileChange{
				path:     filePath,
				contents: []byte(updated),
				mode:     info.Mode().Perm(),
				original: contents,
			})
		}
	}
	return changes, markerFound, nil
}

func applyChanges(changes []fileChange) error {
	applied := make([]fileChange, 0, len(changes))
	for _, change := range changes {
		var err error
		if change.remove {
			err = os.Remove(change.path)
		} else {
			err = atomicWrite(change.path, change.contents, change.mode)
		}
		if err == nil {
			applied = append(applied, change)
			continue
		}
		applyErr := fmt.Errorf("apply template initialization to %s: %w", change.path, err)
		return errors.Join(applyErr, rollbackChanges(applied))
	}
	return nil
}

func rollbackChanges(changes []fileChange) error {
	var rollbackErr error
	for index := len(changes) - 1; index >= 0; index-- {
		change := changes[index]
		if err := atomicWrite(change.path, change.original, change.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", change.path, err))
		}
	}
	return rollbackErr
}

func atomicWrite(path string, contents []byte, mode os.FileMode) (returnErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".template-init-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, removeErr)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return nil
}

func newProject(modulePath string) (project, error) {
	modulePath = strings.TrimSpace(modulePath)
	if err := module.CheckPath(modulePath); err != nil {
		return project{}, fmt.Errorf("invalid Go module path %q: %w", modulePath, err)
	}
	parts := strings.Split(modulePath, "/")
	slug := strings.ToLower(parts[len(parts)-1])
	if len(slug) > 128 || !projectSlugPattern.MatchString(slug) {
		return project{}, fmt.Errorf("repository name %q must contain lowercase letters, numbers, and single '.', '_', or '-' separators", slug)
	}
	return project{
		module:      modulePath,
		slug:        slug,
		database:    databaseIdentifier(slug),
		displayName: displayName(slug),
	}, nil
}

func displayName(slug string) string {
	words := strings.FieldsFunc(slug, func(character rune) bool {
		return character == '-' || character == '_' || character == '.'
	})
	for index, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[index] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

func databaseIdentifier(slug string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, character := range slug {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
			lastUnderscore = false
			continue
		}
		if builder.Len() > 0 && !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	identifier := strings.Trim(builder.String(), "_")
	if identifier == "" {
		return "app"
	}
	return identifier
}

func stripTemplateOnlySections(contents string) (string, error) {
	markers := [][2]string{
		{"<!-- web-app-" + "template:template-only:start -->", "<!-- web-app-" + "template:template-only:end -->"},
		{"# web-app-" + "template:template-only:start", "# web-app-" + "template:template-only:end"},
	}
	for _, pair := range markers {
		for {
			start := strings.Index(contents, pair[0])
			if start < 0 {
				break
			}
			end := strings.Index(contents[start+len(pair[0]):], pair[1])
			if end < 0 {
				return "", errors.New("template-only section is missing its end marker")
			}
			end += start + len(pair[0]) + len(pair[1])
			before := strings.TrimRight(contents[:start], "\r\n")
			after := strings.TrimLeft(contents[end:], "\r\n")
			switch {
			case before == "":
				contents = after
			case after == "":
				contents = before + "\n"
			default:
				contents = before + "\n\n" + after
			}
		}
	}
	return contents, nil
}

func isTemplateOnlyFile(relative string) bool {
	switch filepath.ToSlash(relative) {
	case "cmd/template-init/main.go",
		"docs/research/github-template-repositories.md",
		"internal/templateinit/initialize.go",
		"internal/templateinit/initialize_test.go":
		return true
	default:
		return false
	}
}

func trackedFiles(root string) ([]string, error) {
	command := exec.Command("git", "-C", root, "ls-files", "-z")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w: %s", err, strings.TrimSpace(string(output)))
	}
	items := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(items))
	for _, item := range items {
		if len(item) > 0 {
			files = append(files, string(item))
		}
	}
	return files, nil
}
