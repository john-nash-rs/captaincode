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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/internal/platform"
)

type Registry struct {
	Root                       string
	MaxBytes                   int
	AllowWrites, AllowCommands bool
}

// Definitions lists every tool the harness exposes.
//
// The file operations are written in Go against os.Root and path/filepath, so
// they behave the same on Linux, macOS and Windows: separators, case
// sensitivity and rename semantics are whatever the target filesystem does,
// rather than something this code assumes. That is why they are not shell
// scripts or a Python helper, which would tie the tool surface to an
// interpreter the user may not have installed and to one platform's
// conventions.
//
// Write-capable tools appear only when the matching grant is on. A model that
// cannot see a tool says it has none, which is honest and, before
// /permissions existed, left the operator with nothing to do about it.
func (r Registry) Definitions() []core.Definition {
	definitions := []core.Definition{
		{Name: "read_file", Description: "Read a UTF-8 text file relative to the workspace. Output is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		{Name: "list_files", Description: "List one workspace directory, excluding hidden entries.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		{Name: "search_files", Description: "Search a literal string in bounded workspace files. Returns matching paths.", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)},
		{Name: "stat_file", Description: "Report whether a workspace path exists and whether it is a file, directory, or other.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
	}
	if r.AllowWrites {
		definitions = append(definitions,
			core.Definition{Name: "write_file", Description: "Create or replace a bounded text file. Pass expected_sha256 'absent' to create, or the previous sha256:… to replace an existing file.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path","content","expected_sha256"],"additionalProperties":false}`)},
			core.Definition{Name: "append_file", Description: "Append bounded text to a file, creating it if absent.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)},
			core.Definition{Name: "create_file", Description: "Create an empty file. Fails if it already exists.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
			core.Definition{Name: "make_directory", Description: "Create a directory and any missing parents. Succeeds if it already exists.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
			core.Definition{Name: "move_path", Description: "Rename or move a file or directory inside the workspace.", Parameters: json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"],"additionalProperties":false}`)},
			core.Definition{Name: "delete_path", Description: "Delete a file or an empty directory.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		)
	}
	if r.AllowCommands {
		definitions = append(definitions, core.Definition{Name: "run_command", Description: "Run an absolute executable and argument array inside the OS sandbox. No implicit shell. Output is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}}},"required":["program","args"],"additionalProperties":false}`)})
	}
	return definitions
}

// needsWrite reports whether a tool changes the workspace.
func needsWrite(name string) bool {
	switch name {
	case "write_file", "append_file", "create_file", "make_directory", "move_path", "delete_path":
		return true
	}
	return false
}

// deniedReason explains a refusal in terms the model can pass on.
//
// It used to say only "tool is disabled by current policy", which left the
// model able to tell the user it could not edit anything and nothing more.
// Since the refusal is a deliberate grant rather than a limit, the remedy is
// short and belongs in the message: the model can then tell the user exactly
// what to run, which is the difference between a dead end and a keystroke.
func deniedReason(name string, writes, commands bool) string {
	grant := "/permissions writes on"
	if name == "run_command" {
		grant = "/permissions commands on"
	}
	if needsWrite(name) && !writes {
		return "CaptainCode is read-only, so " + name + " is unavailable. " +
			"Tell the user to run: " + grant + " — it applies immediately, with no restart."
	}
	return "Running programs is disabled. Tell the user to run: " + grant +
		" — it applies immediately, with no restart."
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
	if needsWrite(call.Name) && !r.AllowWrites || call.Name == "run_command" && !r.AllowCommands {
		return core.Failure("denied", deniedReason(call.Name, r.AllowWrites, r.AllowCommands))
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
		temporary := args.Path + ".captaincode-tmp"
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
	case "stat_file":
		var args struct {
			Path string `json:"path"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) {
			return core.Failure("invalid_arguments", "invalid path")
		}
		info, err := root.Stat(args.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return core.Result{Text: "absent"}
		}
		if err != nil {
			return fileError(err)
		}
		kind := "other"
		switch {
		case info.IsDir():
			kind = "directory"
		case info.Mode().IsRegular():
			kind = "file"
		}
		if info.IsDir() {
			return core.Result{Text: "directory"}
		}
		return core.Result{Text: "file " + strconv.FormatInt(info.Size(), 10) + " bytes (" + kind + ")"}

	case "append_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) || args.Path == "." || len(args.Content) > r.MaxBytes {
			return core.Failure("invalid_arguments", "invalid append request")
		}
		existing, _, err := read(root, args.Path, r.MaxBytes)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fileError(err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			existing = nil
		}
		if len(existing)+len(args.Content) > r.MaxBytes {
			return core.Failure("conflict", "the file would exceed the "+strconv.Itoa(r.MaxBytes)+" byte edit limit")
		}
		file, err := root.OpenFile(args.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fileError(err)
		}
		_, writeErr := io.WriteString(file, args.Content)
		syncErr := file.Sync()
		closeErr := file.Close()
		if errors.Join(writeErr, syncErr, closeErr) != nil {
			return core.Failure("write_failed", "append could not be saved")
		}
		return core.Result{OK: true, Text: "appended " + strconv.Itoa(len(args.Content)) + " bytes to " + args.Path}

	case "create_file":
		var args struct {
			Path string `json:"path"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) || args.Path == "." {
			return core.Failure("invalid_arguments", "invalid path")
		}
		file, err := root.OpenFile(args.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, fs.ErrExist) {
			return core.Failure("conflict", "a file already exists at that path")
		}
		if err != nil {
			return fileError(err)
		}
		if err := file.Close(); err != nil {
			return core.Failure("write_failed", "file could not be closed")
		}
		return core.Result{OK: true, Text: "created " + args.Path}

	case "make_directory":
		var args struct {
			Path string `json:"path"`
		}
		// The workspace root already exists and "." would name it, so both are
		// refused rather than treated as a no-op: silently succeeding on the
		// root hides a mistaken path.
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) || args.Path == "." {
			return core.Failure("invalid_arguments", "invalid directory path")
		}
		if err := root.MkdirAll(args.Path, 0o755); err != nil {
			return fileError(err)
		}
		return core.Result{OK: true, Text: "directory ready: " + args.Path}

	case "move_path":
		var args struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if Decode(call.Arguments, &args) != nil || !safePath(args.From) || !safePath(args.To) || args.From == "." || args.To == "." {
			return core.Failure("invalid_arguments", "invalid move request")
		}
		if err := root.Rename(args.From, args.To); err != nil {
			return fileError(err)
		}
		return core.Result{OK: true, Text: "moved " + args.From + " to " + args.To}

	case "delete_path":
		var args struct {
			Path string `json:"path"`
		}
		// Removing the workspace root is refused outright. It is outside the
		// tool's remit and there is no observation that could recover from it.
		if Decode(call.Arguments, &args) != nil || !safePath(args.Path) || args.Path == "." {
			return core.Failure("invalid_arguments", "invalid path")
		}
		info, err := root.Stat(args.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return core.Failure("conflict", "nothing exists at that path")
		}
		if err != nil {
			return fileError(err)
		}
		// A non-empty directory is refused rather than emptied. Recursively
		// deleting whatever happens to be inside a directory is a much larger
		// action than the call asked for, and one that no observation here
		// would make obvious.
		if info.IsDir() {
			entries, err := root.Open(args.Path)
			if err != nil {
				return fileError(err)
			}
			more, _ := entries.Readdirnames(1)
			entries.Close()
			if len(more) > 0 {
				return core.Failure("conflict", "the directory is not empty; delete its contents first")
			}
		}
		if err := root.Remove(args.Path); err != nil {
			return fileError(err)
		}
		return core.Result{OK: true, Text: "deleted " + args.Path}

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

// commandEnv is the environment a model-requested command runs with: nothing
// inherited from the operator's shell, the workspace standing in for the home
// directory, and the platform's own system entries present so a shell can
// start at all.
//
// TMPDIR is the Unix name for the temporary directory. Windows reads TEMP and
// TMP, and a program that cannot write its temporary files reports that as a
// failure of whatever it was asked to do, which is a confusing way to learn
// about a missing variable.
func commandEnv(root string) []string {
	if runtime.GOOS == "windows" {
		return append(platform.ShellEnv(),
			"HOME="+root,
			"USERPROFILE="+root,
			"APPDATA="+root,
			"LOCALAPPDATA="+root,
			"TEMP="+root,
			"TMP="+root,
			"LANG=C",
		)
	}
	return []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + root, "TMPDIR=" + root}
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
	command.Env = commandEnv(r.Root)
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
