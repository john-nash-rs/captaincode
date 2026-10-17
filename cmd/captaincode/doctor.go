package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/john-nash-rs/captaincode/config"
	"github.com/john-nash-rs/captaincode/keyring"
)

// check is one diagnostic result.
//
// Statuses are deliberately coarse. Doctor reports what it can observe; it
// never changes anything, and it never reports a problem as resolved on the
// operator's behalf. A failing sandbox check is the important one: the harness
// refuses to run tools without isolation, and doctor says so rather than
// working around it.
type check struct {
	Name   string
	Status string // ok | warn | fail
	Detail string
	Fix    string
}

// runDoctor prints a diagnostic report and returns an error when any required
// check failed, so it can be used in scripts as a gate.
func runDoctor(args []string) error {
	flags := newFlagSet("doctor")
	asJSON := flags.Bool("json", false, "emit machine-readable JSON")
	configPath := flags.String("config", "", "configuration file to inspect")
	if err := flags.Parse(args); err != nil {
		return err
	}

	checks := []check{platformCheck(), sandboxCheck(*configPath), databaseCheck(), credentialCheck(*configPath)}

	if *asJSON {
		encoder := json.NewEncoder(stdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"version": version,
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
			"checks":  checks,
			"ok":      !hasFailure(checks),
		})
	}

	out := stdout()
	fmt.Fprintf(out, "captaincode %s  (%s/%s)\n\n", version, runtime.GOOS, runtime.GOARCH)
	for _, c := range checks {
		marker := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status]
		fmt.Fprintf(out, "  %s  %-22s %s\n", marker, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(out, "      %s\n", c.Fix)
		}
	}
	fmt.Fprintln(out)
	if hasFailure(checks) {
		fmt.Fprintln(out, "Some required checks failed. CaptainCode will refuse to run tools")
		fmt.Fprintln(out, "without its sandbox rather than running them unsandboxed.")
		return errChecksFailed
	}
	fmt.Fprintln(out, "All required checks passed.")
	return nil
}

var errChecksFailed = fmt.Errorf("one or more doctor checks failed")

func hasFailure(checks []check) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}

func platformCheck() check {
	c := check{Name: "platform", Status: "ok", Detail: runtime.GOOS + "/" + runtime.GOARCH}
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		c.Status = "fail"
		c.Detail = runtime.GOOS + " is not supported"
		c.Fix = "CaptainCode supports macOS and Linux only; the file lock and sandbox worker are platform-specific."
	}
	return c
}

// sandboxCheck verifies the isolation backend this platform actually uses.
// A missing backend is a failure, not a warning: the harness will refuse to
// execute tools, so a successful doctor run must not imply the tool path works
// when it does not.
func sandboxCheck(configPath string) check {
	cfg, err := config.Load(configPath)
	binary := cfg.SandboxBinary
	if binary == "" {
		binary = map[string]string{
			"darwin": "/usr/bin/sandbox-exec",
			"linux":  "bwrap",
		}[runtime.GOOS]
	}
	name := "sandbox"
	switch runtime.GOOS {
	case "darwin":
		name = "sandbox-exec"
	case "linux":
		name = "bubblewrap"
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return check{Name: name, Status: "fail", Detail: "no sandbox backend on this platform"}
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		c := check{Name: name, Status: "fail", Detail: fmt.Sprintf("%s not found", binary)}
		if runtime.GOOS == "linux" {
			c.Fix = "Install it: sudo apt-get install bubblewrap  (Debian/Ubuntu)\n" +
				"      Then confirm unprivileged user namespaces are permitted:\n" +
				"        sysctl kernel.unprivileged_userns_clone"
		} else {
			c.Fix = "Expected the built-in /usr/bin/sandbox-exec. It is deprecated by Apple and\n" +
				"      may be removed by host or MDM policy; tools will refuse to run without it."
		}
		return c
	}

	c := check{Name: name, Status: "ok", Detail: path}
	if runtime.GOOS == "linux" {
		// bubblewrap can be present and still unusable if namespaces are
		// restricted, which is a distinct and common failure on hardened hosts.
		if _, err := os.Stat("/proc/sys/user/max_user_namespaces"); err != nil {
			c.Status = "warn"
			c.Detail = path + " (namespace limits not readable)"
			c.Fix = "If tool execution fails, check user-namespace policy on this host."
		}
	}
	return c
}

// databaseCheck confirms the default session database location is usable. It
// writes nothing; it only proves the directory could be created.
func databaseCheck() check {
	dir, err := os.UserConfigDir()
	if err != nil {
		return check{Name: "database", Status: "warn", Detail: "no user config directory: " + err.Error(),
			Fix: "Pass --db /absolute/path.db explicitly."}
	}
	target := filepath.Join(dir, "captaincode")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		return check{Name: "database", Status: "fail", Detail: target + " exists and is not a directory"}
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return check{Name: "database", Status: "fail", Detail: target + ": " + err.Error(),
			Fix: "Pass --db /absolute/path.db in a writable location outside your workspace."}
	}
	return check{Name: "database", Status: "ok", Detail: target}
}

// credentialCheck reports credential presence without ever revealing it. The
// masked form is produced by the keyring package, which is the only code
// allowed to hold the raw value.
func credentialCheck(configPath string) check {
	if key := strings.TrimSpace(os.Getenv(keyring.EnvVar)); key != "" {
		return check{Name: "credential", Status: "ok", Detail: "OPENROUTER_API_KEY " + keyring.Masked(key)}
	}
	key, err := keyring.Resolve()
	if err != nil {
		return check{Name: "credential", Status: "warn", Detail: "credential store unreadable: " + err.Error()}
	}
	if key == "" {
		path, pathErr := keyring.Path()
		fix := "Run /openrouter api-key inside the interface, or set OPENROUTER_API_KEY.\n" +
			"      The offline fixture provider needs no credential."
		if pathErr == nil {
			fix += "\n      Credential file: " + path
		}
		return check{Name: "credential", Status: "warn", Detail: "not set (offline fixture works without one)", Fix: fix}
	}
	path, _ := keyring.Path()
	return check{Name: "credential", Status: "ok", Detail: keyring.Masked(key) + "  (" + path + ")"}
}

var _ io.Writer = os.Stdout
