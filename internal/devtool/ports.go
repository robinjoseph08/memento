package devtool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// FilePortAllocator coordinates ports between worktrees with advisory locks.
type FilePortAllocator struct {
	LockRoot string
}

type filePortLease struct {
	port int
	file *os.File
}

func (a *FilePortAllocator) Acquire(preferred int) (PortLease, error) {
	lockRoot := a.LockRoot
	if lockRoot == "" {
		env, err := ResolveEnvironment(context.Background())
		if err != nil {
			return nil, err
		}
		lockRoot = filepath.Join(env.MainRoot, "tmp", "ports")
	}
	if err := os.MkdirAll(lockRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create port lock directory: %w", err)
	}

	for port := preferred; port <= 65535; port++ {
		file, err := os.OpenFile(filepath.Join(lockRoot, strconv.Itoa(port)+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open port lock: %w", err)
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = file.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				continue
			}
			return nil, fmt.Errorf("lock port %d: %w", port, err)
		}
		available := portAvailable(port)
		if available {
			return &filePortLease{port: port, file: file}, nil
		}
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}
	return nil, fmt.Errorf("no available port at or above %d", preferred)
}

func (l *filePortLease) Port() int {
	return l.port
}

func (l *filePortLease) Close() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func firstAvailablePort(preferred int) (int, error) {
	for port := preferred; port <= 65535; port++ {
		if portAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port at or above %d", preferred)
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
