package devtool

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func directoryNonempty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read destination files %s: %w", path, err)
	}
	return len(entries) > 0, nil
}

type stagedDirectory struct {
	path           string
	destination    string
	backup         string
	hadDestination bool
	installed      bool
}

func stageDirectory(source, destination string) (*stagedDirectory, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat source directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source %s is not a directory", source)
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create destination parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".files-stage-")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	staged := &stagedDirectory{path: stage, destination: destination}
	if err := copyDirectoryContents(source, stage); err != nil {
		staged.Cleanup()
		return nil, err
	}
	if err := os.Chmod(stage, info.Mode().Perm()); err != nil {
		staged.Cleanup()
		return nil, fmt.Errorf("preserve files directory permissions: %w", err)
	}
	return staged, nil
}

func (s *stagedDirectory) Install() error {
	if s.installed {
		return errors.New("staged files are already installed")
	}
	parent := filepath.Dir(s.destination)
	if _, err := os.Lstat(s.destination); err == nil {
		backup, backupErr := os.MkdirTemp(parent, ".files-backup-")
		if backupErr != nil {
			return fmt.Errorf("create backup path: %w", backupErr)
		}
		if err := os.Remove(backup); err != nil {
			return fmt.Errorf("prepare backup path: %w", err)
		}
		if err := os.Rename(s.destination, backup); err != nil {
			return fmt.Errorf("back up destination: %w", err)
		}
		s.backup = backup
		s.hadDestination = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	if err := os.Rename(s.path, s.destination); err != nil {
		installErr := fmt.Errorf("install cloned files: %w", err)
		if s.backup == "" {
			return installErr
		}
		if restoreErr := os.Rename(s.backup, s.destination); restoreErr != nil {
			return errors.Join(installErr, fmt.Errorf("restore destination after install failure: %w", restoreErr))
		}
		s.backup = ""
		s.hadDestination = false
		return installErr
	}
	s.path = ""
	s.installed = true
	return nil
}

func (s *stagedDirectory) Rollback() error {
	if !s.installed {
		return nil
	}
	if err := os.RemoveAll(s.destination); err != nil {
		return fmt.Errorf("remove installed files: %w", err)
	}
	if s.hadDestination {
		if err := os.Rename(s.backup, s.destination); err != nil {
			return fmt.Errorf("restore replaced files: %w", err)
		}
	}
	s.backup = ""
	s.installed = false
	return nil
}

func (s *stagedDirectory) Finalize() error {
	if !s.installed {
		return errors.New("staged files are not installed")
	}
	if s.backup != "" {
		if err := os.RemoveAll(s.backup); err != nil {
			return fmt.Errorf("remove replaced files: %w", err)
		}
	}
	s.backup = ""
	s.installed = false
	return nil
}

func (s *stagedDirectory) Cleanup() {
	if s != nil && s.path != "" {
		_ = os.RemoveAll(s.path)
	}
}

func copyDirectoryContents(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}

		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case entry.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type at %s", path)
		}
	})
}

func copyFile(source, destination string, mode fs.FileMode) (returnErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, input.Close())
	}()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, output.Close())
	}()

	_, err = io.Copy(output, input)
	return err
}
