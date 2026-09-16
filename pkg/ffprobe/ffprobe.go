// Package ffprobe runs the bundled ffprobe binary against an HTTP video URL
// and returns its embedded chapters. ffprobe reads container headers through
// byte ranges, so a large camera original is never downloaded completely.
package ffprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// Chapter is one read-only navigation segment, in seconds from the start.
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Command locates and bounds the ffprobe process. The zero value uses the
// ffprobe on PATH with a one-minute budget.
type Command struct {
	Path    string
	Timeout time.Duration
}

// Error is a failed probe. Detail is ffprobe's own explanation with the URL
// and every request header value removed, so the message never repeats a key.
type Error struct {
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "ffprobe could not read the video"
	}
	return "ffprobe could not read the video: " + e.Detail
}

const (
	maxOutput = 1 << 20
	// checkTimeout bounds the Settings diagnostic, which a person waits on.
	checkTimeout = 5 * time.Second
)

// binary is the configured ffprobe, or the one on PATH.
func (c Command) binary() string {
	if c.Path == "" {
		return "ffprobe"
	}
	return c.Path
}

// Check runs the binary once with -version so Settings can show that chapter
// extraction has a working ffprobe. It reads no video.
func (c Command) Check(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	binary := c.binary()
	command := exec.CommandContext(ctx, binary, "-version")
	command.Stdout = &limitedWriter{buffer: &stdout}
	command.Stderr = &limitedWriter{buffer: &stderr}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Status{Message: "ffprobe did not respond in time."}
		}
		if _, exited := errors.AsType[*exec.ExitError](err); !exited {
			return Status{Message: "ffprobe was not found or could not start. Check the ffprobe path on the server."}
		}
		return Status{Message: "ffprobe started but did not report its version."}
	}
	version := parseVersion(stdout.String())
	if version == "" {
		return Status{Message: "ffprobe started but did not report its version."}
	}
	return Status{Usable: true, Version: version, Message: "ffprobe is ready to read video chapters."}
}

// parseVersion reads "ffprobe version 7.1.1 Copyright ..." from the first
// line. Distribution builds prefix the number with n, which is dropped.
func parseVersion(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(line)
	if len(fields) < 3 || fields[0] != "ffprobe" || fields[1] != "version" {
		return ""
	}
	return strings.TrimPrefix(fields[2], "n")
}

// Chapters probes url with the given request headers. Missing chapters are an
// empty, successful result.
func (c Command) Chapters(ctx context.Context, url string, headers map[string]string) ([]Chapter, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-hide_banner", "-v", "error", "-print_format", "json", "-show_chapters", "-show_error"}
	var header strings.Builder
	for name, value := range headers {
		header.WriteString(name + ": " + value + "\r\n")
	}
	// ffprobe reads HTTP headers only from its arguments, so the key is visible
	// to processes inside the same container while a probe runs. The container
	// runs Memento alone, and every error path below redacts header values.
	if header.Len() > 0 {
		args = append(args, "-headers", header.String())
	}
	// Sockets that stall for half a minute fail instead of holding the queue,
	// and the probe may open nothing but the HTTP stream it was given: a file
	// that turns out to be a playlist cannot pull ffprobe into other protocols.
	args = append(args, "-seekable", "1", "-rw_timeout", strconv.Itoa(int((30*time.Second)/time.Microsecond)),
		"-protocol_whitelist", "http,https,tcp,tls", "-i", url)
	binary := c.binary()
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedWriter{buffer: &stdout}
	command.Stderr = &limitedWriter{buffer: &stderr}
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	redact := func(text string) string {
		text = strings.ReplaceAll(text, url, "[url]")
		for _, value := range headers {
			if value != "" {
				text = strings.ReplaceAll(text, value, "[redacted]")
			}
		}
		return strings.TrimSpace(text)
	}
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			return nil, errorstack.Capture(fmt.Errorf("ffprobe could not start: %w", &Error{Detail: redact(err.Error())}))
		}
		detail := stderr.String()
		var report struct {
			Error struct {
				String string `json:"string"`
			} `json:"error"`
		}
		if json.Unmarshal(stdout.Bytes(), &report) == nil && report.Error.String != "" {
			detail = report.Error.String
		}
		return nil, errorstack.Capture(&Error{Detail: redact(detail)})
	}
	chapters, err := Parse(stdout.Bytes())
	if err != nil {
		return nil, errorstack.Capture(&Error{Detail: redact(err.Error())})
	}
	return chapters, nil
}

// Parse reads ffprobe's JSON chapter listing. Times arrive as decimal strings.
func Parse(output []byte) ([]Chapter, error) {
	var report struct {
		Chapters []struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
			Tags  struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, fmt.Errorf("unreadable chapter listing")
	}
	chapters := make([]Chapter, 0, len(report.Chapters))
	for _, raw := range report.Chapters {
		start, err := seconds(raw.Start)
		if err != nil {
			return nil, err
		}
		end, err := seconds(raw.End)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, Chapter{Title: strings.TrimSpace(raw.Tags.Title), Start: start, End: end})
	}
	sort.SliceStable(chapters, func(i, j int) bool { return chapters[i].Start < chapters[j].Start })
	return chapters, nil
}

func seconds(value string) (float64, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fmt.Errorf("unreadable chapter time")
	}
	return number, nil
}

// limitedWriter keeps process output bounded so a misbehaving probe cannot
// grow memory without limit.
type limitedWriter struct{ buffer *bytes.Buffer }

func (w *limitedWriter) Write(p []byte) (int, error) {
	if remaining := maxOutput - w.buffer.Len(); remaining > 0 {
		w.buffer.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}
