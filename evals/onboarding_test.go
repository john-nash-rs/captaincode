package evals

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/internal/testenv"
	"github.com/john-nash-rs/captaincode/keyring"
	"github.com/john-nash-rs/captaincode/provider"
)

// These evals drive a live OpenRouter account, so they cost tokens and need a
// real credential. They skip rather than fail when one is absent, which keeps
// them from breaking a checkout that has no key.
//
// The key is read from CAPTAINCODE_EVAL_KEY, or from the file named by
// CAPTAINCODE_EVAL_KEY_FILE. It is never printed, never written to a fixture,
// and never passed on a command line where it could land in a process listing.

const defaultKeyFile = "/Users/harshvardhan/codes/skillcaptain/openrouter.txt"

// keyPattern matches the api-key="…" form the file is stored in. Reading the
// bare value matters: handing the file contents to the provider verbatim would
// send "api-key=\"sk-or-v1-…\"" as the credential and fail as "no key".
var keyPattern = regexp.MustCompile(`(?i)api[-_]?key\s*=\s*"?([^"\s]+)"?`)

// evalKey returns a live credential, or skips the test.
func evalKey(t *testing.T) string {
	t.Helper()
	if key := strings.TrimSpace(os.Getenv("CAPTAINCODE_EVAL_KEY")); key != "" {
		return key
	}
	path := os.Getenv("CAPTAINCODE_EVAL_KEY_FILE")
	if path == "" {
		if _, err := os.Stat(defaultKeyFile); err != nil {
			t.Skip("set CAPTAINCODE_EVAL_KEY or CAPTAINCODE_EVAL_KEY_FILE to run live evals")
		}
		path = defaultKeyFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("key file unreadable: %v", err)
	}
	match := keyPattern.FindSubmatch(data)
	if match == nil {
		// Fall back to the whole file only when it holds a bare key.
		if bare := strings.Trim(strings.TrimSpace(string(data)), `"`); strings.HasPrefix(bare, "sk-or-v1-") {
			return bare
		}
		t.Skipf("no credential found in %s", path)
	}
	return string(match[1])
}

// liveProbe builds a provider that resolves the given credential per request,
// which is what the interface uses and therefore what has to be evaluated.
func liveProbe(t *testing.T, key string) *provider.OpenRouter {
	t.Helper()
	adapter := provider.New(key, "openrouter/free", "")
	adapter.ModelFunc = func() string { return "openrouter/free" }
	return adapter
}

func evalContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The first thing a new user does is press /connect. Its whole promise is that
// a credential and a model are all that is needed, so the check has to succeed
// for a key that works and a model that exists.
func TestLiveCredentialAndModelAreAccepted(t *testing.T) {
	key := evalKey(t)
	results := liveProbe(t, key).Check(evalContext(t), []string{"openrouter/free"})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("a valid key and model were rejected: %v", results[0].Err)
	}
}

// This is the single most valuable check in the set. A new user who typed a
// bad key must be told the key is bad. Told instead that the model is missing,
// they will set a different model and hit the same wall, which is exactly the
// loop that made setup feel broken.
func TestLiveBadCredentialIsReportedAsABadKey(t *testing.T) {
	evalKey(t) // require a working account before testing a broken one
	results := liveProbe(t, "sk-or-v1-0000000000000000000000000000000").Check(evalContext(t), []string{"openrouter/free"})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if !errors.Is(results[0].Err, provider.ErrBadCredential) {
		t.Fatalf("a rejected key reported as %v, want ErrBadCredential", results[0].Err)
	}
}

// The mirror image: a good key with a made-up model must not be reported as a
// credential problem, or the user goes hunting for a key that was never wrong.
func TestLiveUnknownModelIsReportedAsAMissingModel(t *testing.T) {
	key := evalKey(t)
	const absent = "definitely-not-a-real-provider/definitely-not-a-real-model"
	results := liveProbe(t, key).Check(evalContext(t), []string{absent})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if !errors.Is(results[0].Err, provider.ErrUnknownModel) {
		t.Fatalf("a missing model reported as %v, want ErrUnknownModel", results[0].Err)
	}
}

// Several of the identifiers offered as examples must actually resolve, or
// /connect walks a new user into a failure it then has to explain.
func TestLiveSuggestedModelsResolve(t *testing.T) {
	key := evalKey(t)
	// Only the one identifier CaptainCode recommends by default is checked.
	// The rest are third-party slugs that OpenRouter can retire at any time,
	// and a rotating example is worse than no example.
	results := liveProbe(t, key).Check(evalContext(t), []string{"openrouter/free"})
	if len(results) == 0 || results[0].Err != nil {
		t.Fatalf("the default suggestion does not resolve: %v", results)
	}
}

// A real request, end to end: the key is used, a model answers, and the answer
// arrives trimmed. This is the state /connect is supposed to leave the user in.
//
// A named commercial model is used rather than openrouter/free, which routes
// to whatever free model is available and so cannot be asserted on.
func TestLiveRequestReturnsATrimmedAnswer(t *testing.T) {
	key := evalKey(t)
	adapter := provider.New(key, "openai/gpt-4o-mini", "")
	reply, err := adapter.Complete(evalContext(t), []core.Message{
		{Role: "user", Content: "Reply with exactly the word ONLINE and nothing else."},
	}, nil)
	if err != nil {
		t.Fatalf("live request failed: %v", err)
	}
	if !strings.Contains(strings.ToUpper(reply.Content), "ONLINE") {
		t.Fatalf("unexpected answer: %q", reply.Content)
	}
	// Live evals caught providers returning replies that opened with blank
	// lines, which rendered as empty rows above the answer and as stray
	// newlines in one-shot output.
	if reply.Content != strings.TrimSpace(reply.Content) {
		t.Fatalf("answer is not trimmed: %q", reply.Content)
	}
	if strings.HasPrefix(reply.Content, "\n") || strings.HasSuffix(reply.Content, "\n") {
		t.Fatalf("answer carries a leading or trailing newline: %q", reply.Content)
	}
}

// The free tier routes to whatever free model is available, so it is
// non-deterministic, rate limited per account, and partly gated to registered
// harnesses. This asserts the harness copes with all three rather than that any
// particular text comes back.
//
// It previously failed outright on a daily rate limit, which was the eval being
// strict about a condition the service imposes and the user cannot act on
// except by paying.
func TestLiveFreeTierDegradesGracefully(t *testing.T) {
	key := evalKey(t)
	adapter := provider.New(key, "openrouter/free", "")
	for i := range 2 {
		reply, err := adapter.Complete(evalContext(t), []core.Message{
			{Role: "user", Content: "Say hi."},
		}, nil)
		if err != nil {
			// A rate limit or a gating refusal is an acceptable outcome. What
			// must hold is that it is described rather than opaque: the old
			// wording was "inspect redacted transport diagnostics" for every
			// failure alike, which named no cause and no remedy.
			var api *provider.APIError
			if !errors.As(err, &api) {
				t.Fatalf("attempt %d failed with an undescribed error: %v", i+1, err)
			}
			if api.Message == "" {
				t.Errorf("attempt %d gave no cause", i+1)
			}
			if advice := api.Advice(); advice == "" {
				t.Logf("attempt %d: %s (no remedy offered, which is acceptable here)", i+1, api.Message)
			}
			continue
		}
		if reply.Content != strings.TrimSpace(reply.Content) {
			t.Fatalf("attempt %d returned untrimmed text: %q", i+1, reply.Content)
		}
	}
}

// The credential must not be recoverable from anything the check produced. The
// check is displayed in the interface, so a credential leaking into an error
// would put it on screen and in the transcript.
func TestLiveProbeOutputNeverContainsTheCredential(t *testing.T) {
	key := evalKey(t)
	results := liveProbe(t, key).Check(evalContext(t), []string{"openrouter/free", "definitely-not-a-real-provider/definitely-not-a-real-model"})
	var rendered []string
	for _, r := range results {
		rendered = append(rendered, r.Model)
		if r.Err != nil {
			rendered = append(rendered, r.Err.Error())
		}
	}
	joined := strings.Join(rendered, "\n")
	if strings.Contains(joined, key) {
		t.Fatal("the probe output contains the credential")
	}
	if len(key) > 12 && strings.Contains(joined, key[len(key)-4:]) {
		t.Fatalf("the probe output exposes the credential's last four characters: %q", joined)
	}
}

// The complete new-user journey, against a fresh home directory: nothing
// configured, doctor says so, the operator follows what it printed, and the
// next run works with no flags beyond the provider.
//
// This is the eval that would have caught the original problem. Each of the
// pieces is tested elsewhere; what matters here is that they compose, because
// the failure being guarded against was never a bug in any one of them. It was
// a model that could not be named from inside the program.
func TestLiveNewUserJourneyFromNothingToWorking(t *testing.T) {
	key := evalKey(t)
	binary := buildBinary(t)
	home := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte(`{"version":"2.5.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// A run that inherits nothing from the operator's machine. The home and
	// config directory entries come from testenv: naming HOME alone is correct
	// on Unix and does nothing on Windows, where the binary reads USERPROFILE
	// and APPDATA and would have read the real profile of whoever ran this.
	env := append(testenv.ConfigEnv(home),
		"OPENROUTER_API_KEY=",
		"PATH="+os.Getenv("PATH"),
	)

	// Step one: a brand new user asks what is wrong. It must name the problem
	// and a way out, not fail obscurely.
	unconfigured := run(t, binary, env, home, "doctor", "--provider", "openrouter")
	if !strings.Contains(unconfigured, "no model set") {
		t.Fatalf("doctor did not report the missing model:\n%s", unconfigured)
	}

	// Step two: the operator follows the instructions doctor printed.
	configPath := filepath.Join(home, ".captaincode.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"openai/gpt-4o-mini"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeKey(t, home, key)

	// Step three: doctor now confirms the setup without being asked to guess.
	configured := run(t, binary, env, home, "doctor", "--provider", "openrouter")
	if strings.Contains(configured, "no model set") {
		t.Fatalf("doctor still reports an unconfigured model:\n%s", configured)
	}
	if !strings.Contains(configured, "gpt-4o-mini") {
		t.Fatalf("doctor did not read the model back from the settings file:\n%s", configured)
	}
	if !strings.Contains(configured, "All required checks passed") {
		t.Fatalf("doctor did not pass after setup:\n%s", configured)
	}

	// Step four: an ordinary question works, with no --model flag, proving the
	// settings file is what the run actually reads.
	answered := run(t, binary, env, home,
		workspace, "--provider", "openrouter", "--ide=false",
		"--question", "Reply with exactly the word ONLINE and nothing else.")
	if !strings.Contains(strings.ToUpper(answered), "ONLINE") {
		t.Fatalf("the configured run did not produce an answer:\n%s", answered)
	}
	if strings.Contains(answered, key) {
		t.Fatal("the credential appeared in the output")
	}
}

// buildBinary compiles the command once for the journey eval.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), binaryName())
	cmd := exec.Command("go", "build", "-o", binary, "github.com/john-nash-rs/captaincode/cmd/captaincode")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binary
}

// repoRoot is the module directory, which is the parent of this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}

// run executes the built binary with an explicit environment and no inherited
// credential, so the result cannot depend on the developer's shell.
// writeKey stores a credential exactly where keyring.Resolve will read it back.
//
// The directory is asked of keyring rather than assembled here, because it is
// the platform's user config directory: HOME on macOS, XDG_CONFIG_HOME on
// Linux, APPDATA on Windows. Naming it by hand is how an eval comes to pass
// locally and fail on a runner of a different platform.
func writeKey(t *testing.T, home, key string) string {
	t.Helper()
	path := keyring.PathIn(testenv.ConfigDir(home))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, binary string, env []string, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Dir = home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", filepath.Base(binary), args, err, out)
	}
	return string(out)
}

// A key file written the way /connect writes one must be readable by the next
// run, with no environment variable in sight. This is the difference between
// "connected" and "connected until the shell restarted".
func TestLivePersistedCredentialIsUsableWithoutTheEnvironment(t *testing.T) {
	key := evalKey(t)
	home := t.TempDir()
	t.Setenv("OPENROUTER_API_KEY", "") // no environment override

	// Exactly the shape /openrouter api-key writes.
	path := writeKey(t, home, key)

	// Resolve it the way the provider does, then use it.
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	results := liveProbe(t, strings.TrimSpace(string(stored))).Check(evalContext(t), []string{"openrouter/free"})
	if results[0].Err != nil {
		t.Fatalf("the persisted credential did not work: %v", results[0].Err)
	}
}
