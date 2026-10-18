// Package sandbox launches the tool worker under native OS restrictions.
// Its policy is deliberately small: one project, no network, optional writes.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/toolbox"
)

// Grants supplies the current permission set.
//
// It is consulted per request rather than captured at construction, for the
// same reason the credential is: a grant given from the interface must take
// effect without a restart. Freezing them in the Registry meant a user who
// turned writes on still saw the model refuse to edit anything until they quit
// and started again.
type Grants func() (writes, commands bool)

type Worker struct {
	Registry                  toolbox.Registry
	Executable, SandboxBinary string
	// Grants, when set, overrides the Registry's flags on every call.
	Grants Grants
}

// grants resolves the current permission set.
func (w Worker) grants() (writes, commands bool) {
	if w.Grants != nil {
		return w.Grants()
	}
	return w.Registry.AllowWrites, w.Registry.AllowCommands
}

// registry returns the registry with the current grants applied.
func (w Worker) registry() toolbox.Registry {
	writes, commands := w.grants()
	registry := w.Registry
	registry.AllowWrites, registry.AllowCommands = writes, commands
	return registry
}

func (w Worker) Definitions() []core.Definition { return w.registry().Definitions() }
func (w Worker) ReplaySafe(name string) bool    { return w.Registry.ReplaySafe(name) }

// mutating names every tool that changes the workspace. The policy check runs
// here as well as in the registry, so a grant cannot be bypassed by reaching
// the worker directly.
func mutating(name string) bool {
	switch name {
	case "write_file", "append_file", "create_file", "make_directory", "move_path", "delete_path":
		return true
	}
	return false
}

func (w Worker) Execute(ctx context.Context, call core.Call) core.Result {
	registry := w.registry()
	if (mutating(call.Name) && !registry.AllowWrites) || (call.Name == "run_command" && !registry.AllowCommands) {
		// The worker refuses a mutating call before doing anything, so the
		// reason has to carry the remedy too, not merely the fact.
		grant := "/permissions writes on"
		if call.Name == "run_command" {
			grant = "/permissions commands on"
		}
		return core.Failure("denied", "CaptainCode is read-only, so "+call.Name+
			" is unavailable. Tell the user to run: "+grant+
			" — it applies immediately, with no restart.")
	}
	command, err := w.Command(ctx, []string{"worker"})
	if err != nil {
		return core.Failure("sandbox_unavailable", err.Error())
	}
	if runtime.GOOS == "linux" {
		registry.Root = "/workspace"
	}
	input, err := json.Marshal(Envelope{Registry: registry, Call: call})
	if err != nil {
		return core.Failure("invalid_arguments", "cannot encode worker input")
	}
	command.Stdin = bytes.NewReader(input)
	output := &capture{limit: 256 * 1024}
	diagnostics := &capture{limit: 4096}
	command.Stdout = output
	command.Stderr = diagnostics
	err = command.Run()
	if ctx.Err() != nil {
		if !w.ReplaySafe(call.Name) {
			return core.Failure("uncertain", "worker interrupted; effects may have occurred")
		}
		return core.Failure("timeout", "worker interrupted before an observation was returned")
	}
	if err != nil {
		if !w.ReplaySafe(call.Name) {
			return core.Failure("uncertain", "worker exited without a trustworthy observation; effects may have occurred")
		}
		return core.Failure("sandbox_failed", "worker failed; no unsandboxed fallback was attempted")
	}
	if output.truncated {
		if !w.ReplaySafe(call.Name) {
			return core.Failure("uncertain", "worker response exceeded limit after a possible effect")
		}
		return core.Failure("protocol", "worker output exceeded limit")
	}
	var result core.Result
	if err := json.Unmarshal(output.buffer.Bytes(), &result); err != nil {
		if !w.ReplaySafe(call.Name) {
			return core.Failure("uncertain", "worker response was invalid after a possible effect")
		}
		return core.Failure("protocol", "invalid worker response")
	}
	return result
}

type Envelope struct {
	Registry toolbox.Registry `json:"registry"`
	Call     core.Call        `json:"call"`
}

func (w Worker) Command(ctx context.Context, workerArgs []string) (*exec.Cmd, error) {
	root, err := filepath.EvalSymlinks(w.Registry.Root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(w.Executable)
	if err != nil {
		return nil, err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, err
	}
	if root == "/" {
		return nil, errors.New("workspace cannot be the filesystem root")
	}
	var command *exec.Cmd
	// Resolved once, here, and used by both backends.
	//
	// Reading the Registry's startup fields instead meant a grant given from
	// the interface changed the advertised tool list and the policy check, but
	// not the isolation profile: the model saw create_file, called it, passed
	// the check, and was then refused by sandbox-exec. To an operator that
	// reads as "the system did not allow it", which is exactly the report.
	registry := w.registry()

	switch runtime.GOOS {
	case "darwin":
		binary := w.SandboxBinary
		if binary == "" {
			binary = "/usr/bin/sandbox-exec"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return nil, errors.New("sandbox-exec unavailable")
		}
		// The live grant, not the registry's startup value. Reading the frozen
		// field here meant a grant given from the interface still launched a
		// read-only profile: the tool was advertised, the call passed the
		// policy check, and sandbox-exec then refused the write, which reads
		// to the operator as "the system did not allow it".
		profile := DarwinProfile(root, executable, registry.AllowWrites)
		args := append([]string{"-p", profile, executable}, workerArgs...)
		command = exec.CommandContext(ctx, binary, args...)
	case "linux":
		binary := w.SandboxBinary
		if binary == "" {
			binary = "bwrap"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return nil, errors.New("bubblewrap unavailable; install it before running tools")
		}
		args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp"}
		for _, directory := range []string{"/usr", "/bin", "/lib", "/lib64"} {
			if _, err := os.Stat(directory); err == nil {
				args = append(args, "--ro-bind", directory, directory)
			}
		}
		mode := "--ro-bind"
		if registry.AllowWrites {
			mode = "--bind"
		}
		args = append(args, mode, root, "/workspace")
		for _, name := range []string{".git", ".env", ".ssh", ".aws", ".codex"} {
			info, err := os.Lstat(filepath.Join(root, name))
			if err != nil {
				continue
			}
			target := "/workspace/" + name
			if info.IsDir() {
				args = append(args, "--tmpfs", target, "--remount-ro", target)
			} else if info.Mode()&os.ModeSymlink == 0 {
				args = append(args, "--ro-bind", "/dev/null", target)
			}
		}
		args = append(args, "--ro-bind", executable, "/captaincode", "--chdir", "/workspace", "--", "/captaincode")
		args = append(args, workerArgs...)
		command = exec.CommandContext(ctx, binary, args...)
	default:
		return nil, errors.New("sandbox worker supports macOS and Linux only")
	}
	command.Dir = root
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=/nonexistent"}
	isolate(command)
	command.WaitDelay = 2 * time.Second
	return command, nil
}

// DarwinProfile allows platform libraries and the exact worker binary to be
// read, plus the selected workspace. No network capability is granted.
func DarwinProfile(root, executable string, writable bool) string {
	quote := strconv.Quote
	var rules []string
	rules = append(rules, "(version 1)", "(deny default)", "(allow process-exec)", "(allow process-fork)", "(allow signal (target self))", "(allow sysctl-read)", "(allow file-read* (literal \"/\"))")
	for _, path := range []string{"/usr", "/bin", "/sbin", "/System", "/Library/Apple", "/private/var/db/dyld", root} {
		rules = append(rules, "(allow file-read* (subpath "+quote(path)+"))")
	}
	rules = append(rules, "(allow file-read* (literal "+quote(executable)+"))", "(allow file-read-metadata)", "(allow file-read* file-write* (literal \"/dev/null\"))", "(allow file-read* (literal \"/dev/urandom\"))")
	if writable {
		rules = append(rules, "(allow file-write* (subpath "+quote(root)+"))")
	}
	for _, name := range []string{".git", ".env", ".ssh", ".aws", ".codex"} {
		rules = append(rules, fmt.Sprintf("(deny file-read* file-write* (subpath %s))", quote(filepath.Join(root, name))))
	}
	return strings.Join(rules, "\n")
}

type capture struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (c *capture) Write(data []byte) (int, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	n := len(data)
	remaining := c.limit - c.buffer.Len()
	if n > remaining {
		data = data[:remaining]
		c.truncated = true
	}
	c.buffer.Write(data)
	return n, nil
}
