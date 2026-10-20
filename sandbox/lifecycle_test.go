package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/toolbox"
)

// requireBackend skips a test on a platform with no isolation backend.
//
// The tests below probe real OS enforcement. Without a backend there is
// nothing to enforce, and they would do no more than assert that the refusal
// names a path that does not exist on this platform. Skipping is the honest
// report; running would be a green or red that means nothing.
func requireBackend(t *testing.T) {
	t.Helper()
	if !HasBackend() {
		t.Skipf("no sandbox backend on %s", runtime.GOOS)
	}
}

// shellFor is the program a probe runs to exercise a boundary.
//
// Every boundary below is enforced by the OS on a process, not by a shell, so
// the shell is only what writes the bytes. Using the platform's own means the
// probes run on Windows too, which is the point: a boundary asserted only on
// the platforms it was written on is a boundary nobody has checked.
func shellFor(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = os.Getenv("WINDIR")
		}
		if root == "" {
			root = `C:\Windows`
		}
		return filepath.Join(root, "System32", "cmd.exe")
	}
	return "/bin/sh"
}

// shellRun is the argv that runs one script string under shellFor.
//
// The two shells do not take arguments the same way, and the difference is
// not a detail. `sh -c script name arg` binds `$0` to name and `$1` upwards to
// the arguments that follow, which is why the Unix scripts can read a path as
// `"$1"`. `cmd /c` has no such binding: it is documented that batch parameters
// do not exist at the command line, so a script run through /c sees `%1` as an
// empty string, and every probe that passed a path that way was really running
// a command with a missing argument. On Windows the arguments are therefore
// substituted into the script before cmd ever reads it, which is why the
// Windows scripts here also spell their placeholder `%~1`.
func shellRun(t *testing.T, program, script string, args ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return []string{"/c", substituteWindowsArgs(script, args)}
	}
	return append([]string{"-c", script, program}, args...)
}

// substituteWindowsArgs replaces `%1`, `%2` … and their `%~` forms with the
// arguments, because cmd has no way to bind them itself at the command line.
//
// It is separate from shellRun so it can be tested on every platform: the
// branch it belongs to only runs on Windows, and a rule that is only ever
// exercised on one operating system is a rule that is wrong on two.
func substituteWindowsArgs(script string, args []string) string {
	for i := len(args); i >= 1; i-- {
		// The value substitutes bare so each caller can quote it according to
		// the shell syntax it uses.
		value := args[i-1]
		// The tilde form is replaced first so its own `%1` is not caught by
		// the plain rule.
		script = strings.ReplaceAll(script, fmt.Sprintf("%%~%d", i), value)
		script = strings.ReplaceAll(script, fmt.Sprintf("%%%d", i), value)
	}
	return script
}

// The probes lean on substituteWindowsArgs being exact, and it runs on
// platforms this test can execute even though its caller's branch only runs on
// Windows. An unsubstituted placeholder is a command that fails for a reason
// that has nothing to do with the boundary under test.
func TestSubstituteWindowsArgs(t *testing.T) {
	cases := []struct {
		name   string
		script string
		args   []string
		want   string
	}{
		{"read probe", `type "%~1"`, []string{`C:\t\inside.txt`}, `type "C:\t\inside.txt"`},
		{"write probe", `echo changed>"%~1"`, []string{`C:\t\new.txt`}, `echo changed>"C:\t\new.txt"`},
		{"path with a space", `type "%~1"`, []string{`C:\a b\x.txt`}, `type "C:\a b\x.txt"`},
		{"plain form", `echo x>>"%1"`, []string{`C:\t\h`}, `echo x>>"C:\t\h"`},
		{"second argument", `echo %1 %2`, []string{"one", "two"}, "echo one two"},
		{"no arguments", `type "%~1"`, nil, `type "%~1"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := substituteWindowsArgs(c.script, c.args); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestOSNetworkAndEnvironmentBoundary(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("network-canary")) }))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	// The probe is a program whose only job is to reach a socket that is
	// certainly listening. curl does it on Unix; powershell does it on
	// Windows, and both are no-ops if the network is genuinely gone, which is
	// what the assertion reads.
	probe, args := networkProbe(t, server.URL)
	worker := Worker{Executable: probe, Registry: toolbox.Registry{Root: t.TempDir(), MaxBytes: 512}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, err := worker.Command(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.CombinedOutput()
	if err == nil && strings.Contains(string(output), "network-canary") {
		t.Fatal("worker reached local network", string(output))
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "OPENROUTER_API_KEY=") {
			t.Fatal("model key inherited by worker")
		}
	}
}

// networkProbe returns a program and its arguments that would print the
// canary if the network were reachable.
func networkProbe(t *testing.T, url string) (string, []string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/usr/bin/curl", []string{"--silent", "--max-time", "5", url}
	}
	// PowerShell rather than curl: a curl.exe on PATH on Windows is an
	// unrelated program (the Git-for-Windows one is a shim), and this needs
	// to be the interpreter whose absence or failure is unambiguous.
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	powershell := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	script := "(New-Object System.Net.WebClient).DownloadString('" + url + "')"
	return powershell, []string{"-NoProfile", "-NonInteractive", "-Command", script}
}

func TestOSCancellationStopsHeartbeat(t *testing.T) {
	requireBackend(t)
	if os.Getenv("CAPTAINCODE_SANDBOX_TEST") != "1" {
		t.Skip("OS probes require explicit opt-in")
	}
	root := t.TempDir()
	heartbeat := filepath.Join(root, "heartbeat")
	guest := heartbeat
	if runtime.GOOS == "linux" {
		guest = "/workspace/heartbeat"
	}
	// A loop that writes until it is stopped. On Windows this is cmd's own
	// loop rather than a shell's, so the boundary being tested is the
	// cancellation and not the presence of sh.
	var script string
	if runtime.GOOS == "windows" {
		// t.TempDir paths on the hosted Windows runner contain no spaces. Keep
		// the path unquoted because cmd.exe parses /c text differently from the
		// C-runtime quoting used to compose the CreateProcess command line.
		script = `for /l %i in (1,1,1000000) do @(echo x>>%1 & ping -n 1 127.0.0.1 >nul)`
	} else {
		script = `while true; do printf x >> "$1"; sleep 0.03; done`
	}
	worker := Worker{Executable: shellFor(t), Registry: toolbox.Registry{Root: root, MaxBytes: 512, AllowWrites: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arguments := shellRun(t, "heartbeat", script, guest)
	command, err := worker.Command(ctx, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(heartbeat)
		if len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("worker never started heartbeat")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not terminate")
	}
	before, _ := os.ReadFile(heartbeat)
	time.Sleep(200 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if len(before) != len(after) {
		t.Fatal("work continued after cancellation")
	}
}
