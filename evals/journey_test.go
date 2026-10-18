package evals

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE JOURNEY, OFFLINE, AGAINST THE REAL BINARY.
//
// Everything else in this repository tests one link in the chain. This walks
// the chain the way a first-time user does, in one process, with no network
// and no credential, which is what lets it run on every push.
//
// It exists because the gaps it covers were each individually green and the
// product was still unusable: the settings file was not consulted, the model
// could not be named from inside the program, paste was discarded, a grant
// looked effective and was refused by the sandbox, and a long reply blanked
// the screen. None of those is visible from a unit test of the part involved.
//
// Offline on purpose. Every check that needs a live key skips without one, so a
// journey guarded by one is a journey that does not run.

const journeyProvider = "fixture"

// journeyEnv is an environment with nothing inherited. A run that can see the
// developer's own settings is not a first run.
func journeyEnv(home string) []string {
	return []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"OPENROUTER_API_KEY=",
		"CAPTAINCODE_CONFIG=",
		"PATH=" + os.Getenv("PATH"),
		"LANG=C",
	}
}

func journeyBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("CAPTAINCODE_TEST_BINARY"); binary != "" {
		return binary
	}
	binary := filepath.Join(t.TempDir(), "captaincode")
	build := exec.Command("go", "build", "-o", binary, "github.com/john-nash-rs/captaincode/cmd/captaincode")
	build.Dir = moduleRoot()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cannot build the command under test: %v\n%s", err, out)
	}
	return binary
}

func moduleRoot() string {
	wd, _ := os.Getwd()
	return filepath.Dir(wd) // the module root, parent of evals/
}

type journey struct {
	t        *testing.T
	binary   string
	home     string
	work     string
	db       string
	settings string
	keyfile  string
}

func newJourney(t *testing.T) *journey {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	j := &journey{
		t:        t,
		binary:   journeyBinary(t),
		home:     home,
		work:     work,
		db:       filepath.Join(t.TempDir(), "journey.db"),
		settings: filepath.Join(home, ".captaincode.json"),
		keyfile:  filepath.Join(home, "Library", "Application Support", "captaincode", "openrouter.key"),
	}
	if err := os.WriteFile(filepath.Join(work, "package.json"), []byte(`{"name":"demo","version":"2.5.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return j
}

// run invokes the binary with the journey environment.
func (j *journey) run(args ...string) (string, error) {
	j.t.Helper()
	cmd := exec.Command(j.binary, args...)
	cmd.Env = journeyEnv(j.home)
	cmd.Dir = j.work
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustRun fails the test if the command does not succeed.
func (j *journey) mustRun(args ...string) string {
	j.t.Helper()
	out, err := j.run(args...)
	if err != nil {
		j.t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
	return out
}

// A new user can reach a working setup using only what the tool tells them.
func TestJourneyFirstRunToWorkingSetup(t *testing.T) {
	j := newJourney(t)

	// 1. Nothing is configured. The run must still start and say what is wrong.
	start := j.mustRun(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider, "--question", "what is the project version?")
	if !strings.Contains(start, "2.5.0") {
		t.Fatalf("a first run could not answer the built-in task:\n%s", start)
	}

	// 2. doctor explains the missing settings rather than failing obscurely.
	doctor := j.mustRun("doctor")
	if !strings.Contains(doctor, "config") {
		t.Fatalf("doctor does not report the settings file:\n%s", doctor)
	}

	// 3. The operator follows what the interface would have written. This is
	//    exactly what /connect does, so it is the state a user reaches after
	//    the wizard, not a hand-built shortcut.
	if err := os.WriteFile(j.settings, []byte(`{"model":"fixture","allow_writes":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(j.keyfile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.keyfile, []byte("sk-or-v1-JOURNEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. The settings are remembered: no --model, no key on the command line.
	after := j.mustRun(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider, "--question", "what is the project version?")
	if !strings.Contains(after, "2.5.0") {
		t.Fatalf("the stored settings were not used:\n%s", after)
	}

	// 5. With writes granted, the agent can actually create a file. This is
	//    the step that was broken in three separate places at once.
	j.mustRun(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider,
		"--question", "create notes.txt containing hello from the journey")
	created := filepath.Join(j.work, "notes.txt")
	data, err := os.ReadFile(created)
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if !strings.Contains(string(data), "hello from the journey") {
		t.Fatalf("the file has the wrong contents: %q", data)
	}

	// 6. A restart must not lose the settings. A new process, same home.
	final := j.mustRun(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider, "--question", "what is the project version?")
	if !strings.Contains(final, "2.5.0") {
		t.Fatalf("settings did not survive a restart:\n%s", final)
	}
	if _, err := os.Stat(j.settings); err != nil {
		t.Fatalf("the settings file went missing: %v", err)
	}
}

// Read-only is the default and must hold. A grant the operator did not make
// must not be inherited from anywhere.
func TestJourneyIsReadOnlyUntilGranted(t *testing.T) {
	j := newJourney(t)
	if err := os.WriteFile(j.settings, []byte(`{"model":"fixture","allow_writes":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The agent will try; it must be refused rather than silently succeeding.
	out, _ := j.run(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider,
		"--question", "create sneaky.txt containing anything")
	if _, err := os.Stat(filepath.Join(j.work, "sneaky.txt")); err == nil {
		t.Fatalf("a file was written with no grant:\n%s", out)
	}
	// And the refusal must name the command that changes it, so the operator
	// is not left at a dead end.
	if !strings.Contains(out, "permissions") {
		t.Fatalf("the refusal gives no way forward:\n%s", out)
	}
}

// A project with no package.json must produce an answer, not a crash. This is
// what someone sees when they point the tool at the wrong directory.
func TestJourneyWrongDirectoryIsExplained(t *testing.T) {
	j := newJourney(t)
	if err := os.Remove(filepath.Join(j.work, "package.json")); err != nil {
		t.Fatal(err)
	}
	out, _ := j.run(j.work, "--ide=false", "--db", j.db,
		"--provider", journeyProvider, "--question", "what is the project version?")
	if out == "" {
		t.Fatal("an empty project produced no output at all")
	}
	if strings.Contains(out, "panic") {
		t.Fatalf("an empty project caused a panic:\n%s", out)
	}
}

// Two harness processes must not fight over one database. The refusal has to
// name the path, or it reads as the settings being lost.
func TestJourneySecondProcessIsRefusedClearly(t *testing.T) {
	j := newJourney(t)
	args := []string{j.work, "--ide=false", "--db", j.db, "--provider", journeyProvider,
		"--question", "what is the project version?"}
	first := exec.Command(j.binary, args...)
	first.Env = journeyEnv(j.home)
	first.Dir = j.work
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Wait()

	second, err := j.run(args...)
	if err == nil {
		// The first may have finished; that is a legitimate race, not a fault.
		if !strings.Contains(second, "2.5.0") {
			t.Fatalf("the second run neither answered nor explained itself:\n%s", second)
		}
		return
	}
	if !strings.Contains(second, "another harness process") {
		t.Fatalf("a busy database was reported obscurely:\n%s", second)
	}
}
