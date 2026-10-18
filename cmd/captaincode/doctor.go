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
	provider := flags.String("provider", "fixture", "provider the model is checked against")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, resolved, cfgErr := config.Resolve(*configPath)
	checks := []check{platformCheck(), versionCheck(), configCheck(cfg, resolved, cfgErr, *provider), sandboxCheck(cfg), databaseCheck(), credentialCheck()}

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
	if failed := failures(checks); len(failed) > 0 {
		// Name what actually failed. The explanation used to be fixed text
		// about the sandbox, which was true while that was the only check
		// able to fail and became a red herring as soon as another could.
		fmt.Fprintf(out, "Failed: %s.\n", strings.Join(failed, ", "))
		fmt.Fprintln(out, "CaptainCode will not work around these.")
		return errChecksFailed
	}
	fmt.Fprintln(out, "All required checks passed.")
	return nil
}

// suggestedModels are offered when setting a model. They are a starting point,
// not a curated list: anyone can read a current identifier off openrouter.ai,
// so pinning a hardcoded set here would rot and look authoritative while doing
// it. The free tier is listed first because it costs nothing to try.
var suggestedModels = []string{
	"openrouter/free",
	"openai/gpt-4o-mini",
	"anthropic/claude-sonnet-4.5",
	"google/gemini-2.0-flash-001",
	"deepseek/deepseek-chat",
}

var errChecksFailed = fmt.Errorf("one or more doctor checks failed")

func hasFailure(checks []check) bool { return len(failures(checks)) > 0 }

// failures lists the names of the checks that failed, in report order.
func failures(checks []check) []string {
	var failed []string
	for _, c := range checks {
		if c.Status == "fail" {
			failed = append(failed, c.Name)
		}
	}
	return failed
}

func platformCheck() check {
	c := check{Name: "platform", Status: "ok", Detail: runtime.GOOS + "/" + runtime.GOARCH}
	switch runtime.GOOS {
	case "darwin", "linux":
	case "windows":
		// The binary builds and the database lock works here, but there is no
		// isolation backend, so tools cannot run. Saying "ok" would be the one
		// thing worse than not building at all.
		c.Status = "fail"
		c.Detail = "windows builds, but has no sandbox backend yet"
		c.Fix = "Talking to a model works on Windows.\n" +
			"      Running tools does not: there is no isolation backend, and\n" +
			"      CaptainCode refuses to execute tools unsandboxed."
	default:
		c.Status = "fail"
		c.Detail = runtime.GOOS + " is not supported"
		c.Fix = "CaptainCode supports macOS and Linux; Windows is partial."
	}
	return c
}

// sandboxCheck verifies the isolation backend this platform actually uses.
// A missing backend is a failure, not a warning: the harness will refuse to
// execute tools, so a successful doctor run must not imply the tool path works
// when it does not.
func sandboxCheck(cfg config.Config) check {
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

// configCheck reports which configuration file is in force and whether it names
// a model.
//
// An empty model is reported rather than left implicit. The failure it causes
// arrives from deep inside a provider call as "configure an explicit OpenRouter
// model ID", which names none of the places a model can come from; naming the
// file that was searched for, and the ways to set one, is the difference
// between a one-line fix and an afternoon.
func configCheck(cfg config.Config, resolved string, err error, provider string) check {
	if err != nil {
		return check{Name: "config", Status: "fail", Detail: err.Error(), Fix: "Correct the file, or run with --config <path>."}
	}
	c := check{Name: "config", Status: "ok", Detail: resolved}
	switch {
	case cfg.Model != "":
		c.Detail += "  model " + cfg.Model
	case provider == "fixture":
		c.Detail += "  no model set (the fixture provider needs none)"
	default:
		c.Status = "warn"
		c.Detail += "  no model set"
		c.Fix = "Set one here:  echo '{\"model\":\"openrouter/free\"}' > " + resolved + "\n" +
			"      Or for a single run:  captaincode --provider " + provider + " --model openrouter/free .\n" +
			"      Or inside the interface:  /openrouter model openrouter/free"
	}
	return c
}

// versionCheck catches the single most confusing thing a returning user hits:
// running an older release than the one that added config auto-loading, so
// every run starts blind and looks as though settings were never saved.
//
// This bit for several rounds of development, and nothing said so, because a
// stale binary behaves exactly like a broken feature.
func versionCheck() check {
	c := check{Name: "version", Status: "ok", Detail: version}
	if version != "dev" {
		return c
	}
	c.Detail = version + " (unreleased build)"
	if release, err := newestRelease(); err == nil && release != "" && release != version {
		c.Status = "warn"
		c.Detail += "  ·  release " + release + " is newer"
		c.Fix = "You are running a locally built binary. A published release may differ:\n" +
			"      captaincode version"
	}
	return c
}

// credentialCheck reports credential presence without ever revealing it. The
// masked form is produced by the keyring package, which is the only code
// allowed to hold the raw value.
func credentialCheck() check {
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
