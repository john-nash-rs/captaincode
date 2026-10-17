package toolbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"example.com/agentbook/core"
)

type Registry struct {
	Root                       string
	MaxBytes                   int
	AllowWrites, AllowCommands bool
}

func (r Registry) Definitions() []core.Definition {
	definitions := []core.Definition{
		{Name: "read_file", Description: "Read a UTF-8 text file relative to the workspace. Output is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		{Name: "list_files", Description: "List one workspace directory, excluding hidden entries.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		{Name: "search_files", Description: "Search a literal string in bounded workspace files. Returns matching paths.", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)},
	}
	if r.AllowWrites {
		definitions = append(definitions, core.Definition{Name: "write_file", Description: "Replace a bounded text file only when its SHA-256 equals expected_sha256; use absent to create.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path","content","expected_sha256"],"additionalProperties":false}`)})
	}
	if r.AllowCommands {
		definitions = append(definitions, core.Definition{Name: "run_command", Description: "Run an absolute executable and argument array inside the OS sandbox. No implicit shell. Output is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}}},"required":["program","args"],"additionalProperties":false}`)})
	}
	return definitions
}

func (r Registry) ReplaySafe(name string) bool {
	return name == "read_file" || name == "list_files" || name == "search_files"
}

func Decode(data []byte, target any) error {
	if len(data) > 32768 {
		return errors.New("arguments too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func safePath(path string) bool {
	if path == "." {
		return true
	}
	if path == "" || len(path) > 4096 || !filepath.IsLocal(path) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") || part == "node_modules" {
			return false
		}
	}
	return true
}

func (r Registry) Execute(ctx context.Context, call core.Call) core.Result {
	if err := ctx.Err(); err != nil {
		return core.Failure("cancelled", "operation cancelled")
	}
	if r.MaxBytes < 128 || r.MaxBytes > 32768 {
		return core.Failure("configuration", "invalid output limit")
	}
	if (call.Name == "write_file" && !r.AllowWrites) || (call.Name == "run_command" && !r.AllowCommands) {
		return core.Failure("denied", "tool is disabled by current policy")
	}
	if call.Name == "run_command" {
		return r.command(ctx, call)
	}
	root, err := os.OpenRoot(r.Root)
	if err != nil {
		return core.Failure("workspace", "workspace unavailable")
	}
	defer root.Close()
	switch call.Name {
	case "read_file", "list_files":
		var args struct {
			Path string `json:"path"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) {
			return core.Failure("invalid_arguments", "path must be a visible workspace-relative path")
		}
		if call.Name == "list_files" {
			file, err := root.Open(args.Path)
			if err != nil {
				return fileError(err)
			}
			defer file.Close()
			entries, err := file.ReadDir(201)
			if err != nil && err != io.EOF {
				return fileError(err)
			}
			var names []string
			for _, entry := range entries {
				if safePath(entry.Name()) && entry.Type()&os.ModeSymlink == 0 {
					names = append(names, entry.Name())
				}
			}
			return bounded(strings.Join(names, "\n"), r.MaxBytes, len(entries) > 200)
		}
		data, truncated, err := read(root, args.Path, r.MaxBytes)
		if err != nil {
			return fileError(err)
		}
		if !truncated && !utf8.Valid(data) {
			return core.Failure("invalid_encoding", "file is not valid UTF-8 text")
		}
		result := bounded(string(data), r.MaxBytes, truncated)
		if !truncated {
			hash := sha256.Sum256(data)
			result.SHA256 = hex.EncodeToString(hash[:])
		}
		return result
	case "search_files":
		var args struct {
			Query string `json:"query"`
		}
		if Decode(call.Arguments, &args) != nil || args.Query == "" || len(args.Query) > 256 {
			return core.Failure("invalid_arguments", "query must be 1–256 bytes")
		}
		var matches []string
		visited := 0
		truncated := false
		err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				return nil
			}
			if !safePath(path) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			visited++
			if visited > 1000 || len(matches) >= 100 {
				truncated = true
				return fs.SkipAll
			}
			data, cut, err := read(root, path, r.MaxBytes)
			if cut {
				truncated = true
			}
			if err == nil && bytes.Contains(data, []byte(args.Query)) {
				matches = append(matches, path)
			}
			return nil
		})
		if err != nil {
			return core.Failure("cancelled", "search interrupted")
		}
		return bounded(strings.Join(matches, "\n"), r.MaxBytes, truncated)
	case "write_file":
		var args struct {
			Path     string  `json:"path"`
			Content  *string `json:"content"`
			Expected string  `json:"expected_sha256"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) || args.Path == "." || args.Content == nil || args.Expected == "" || len(*args.Content) > r.MaxBytes {
			return core.Failure("invalid_arguments", "invalid write request")
		}
		old, cut, err := read(root, args.Path, r.MaxBytes)
		if errors.Is(err, fs.ErrNotExist) {
			if args.Expected != "absent" {
				return core.Failure("conflict", "file is absent")
			}
		} else if err != nil {
			return fileError(err)
		} else {
			hash := sha256.Sum256(old)
			if cut || hex.EncodeToString(hash[:]) != args.Expected {
				return core.Failure("conflict", "file changed or exceeds edit limit")
			}
		}
		// The application is a single writer. Other editors can still race this
		// check; the book documents that this is not an OS compare-and-swap.
		temporary := args.Path + ".agentbook-tmp"
		file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fileError(err)
		}
		defer root.Remove(temporary)
		_, writeErr := io.WriteString(file, *args.Content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if errors.Join(writeErr, syncErr, closeErr) != nil {
			return core.Failure("write_failed", "temporary file could not be saved")
		}
		if err := ctx.Err(); err != nil {
			return core.Failure("cancelled", "write cancelled before rename")
		}
		if err := root.Rename(temporary, args.Path); err != nil {
			return fileError(err)
		}
		hash := sha256.Sum256([]byte(*args.Content))
		return core.Result{OK: true, Text: "sha256:" + hex.EncodeToString(hash[:])}
	default:
		return core.Failure("unknown_tool", "tool is not registered")
	}
}

func read(root *os.Root, path string, limit int) ([]byte, bool, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if len(data) > limit {
		return data[:limit], true, err
	}
	return data, false, err
}

func bounded(text string, limit int, truncated bool) core.Result {
	text = strings.ToValidUTF8(text, "�")
	if len(text) > limit {
		text = text[:limit]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		truncated = true
	}
	return core.Result{OK: true, Text: text, Truncated: truncated}
}

func fileError(err error) core.Result {
	if errors.Is(err, fs.ErrNotExist) {
		return core.Failure("not_found", "path not found")
	}
	if errors.Is(err, fs.ErrPermission) {
		return core.Failure("denied", "filesystem access denied")
	}
	return core.Failure("filesystem", "filesystem operation failed")
}

type limitedWriter struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	n := len(data)
	remaining := w.limit - w.buffer.Len()
	if len(data) > remaining {
		data = data[:remaining]
		w.truncated = true
	}
	w.buffer.Write(data)
	return n, nil
}

func (r Registry) command(ctx context.Context, call core.Call) core.Result {
	var args struct {
		Program string   `json:"program"`
		Args    []string `json:"args"`
	}
	if Decode(call.Arguments, &args) != nil || !filepath.IsAbs(args.Program) || args.Args == nil || len(args.Args) > 64 {
		return core.Failure("invalid_arguments", "program must be absolute and args an array")
	}
	command := exec.CommandContext(ctx, args.Program, args.Args...)
	command.Dir = r.Root
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + r.Root, "TMPDIR=" + r.Root}
	output := &limitedWriter{limit: r.MaxBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if ctx.Err() != nil {
		return core.Failure("uncertain", "command interrupted; effects may have occurred")
	}
	if err != nil {
		result := bounded(fmt.Sprintf("%v\n%s", err, output.buffer.String()), r.MaxBytes, output.truncated)
		result.OK, result.Code = false, "uncertain"
		return result
	}
	return bounded(output.buffer.String(), r.MaxBytes, output.truncated)
}
