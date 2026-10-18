package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeChecksums(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fourAssets = `aaa1  captaincode_0.3.2_darwin_arm64.tar.gz
bbb2  captaincode_0.3.2_darwin_amd64.tar.gz
ccc3  captaincode_0.3.2_linux_arm64.tar.gz
ddd4  captaincode_0.3.2_linux_amd64.tar.gz
`

// Every digest in the cask must be the one published for that exact asset.
// A cask that names the right artifact with the wrong digest fails at install
// time, on a user's machine, with an unhelpful checksum error.
func TestEachDigestMatchesItsOwnAsset(t *testing.T) {
	sums, err := parseChecksums(writeChecksums(t, fourAssets))
	if err != nil {
		t.Fatal(err)
	}
	cask, err := render("v0.3.2", sums)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"captaincode_0.3.2_darwin_arm64.tar.gz": "aaa1",
		"captaincode_0.3.2_darwin_amd64.tar.gz": "bbb2",
		"captaincode_0.3.2_linux_arm64.tar.gz":  "ccc3",
		"captaincode_0.3.2_linux_amd64.tar.gz":  "ddd4",
	}
	for asset, digest := range want {
		marker := "sha256 \"" + digest + "\""
		index := strings.Index(cask, marker)
		if index < 0 {
			t.Fatalf("%s digest is absent", asset)
		}
		// The url on the following line must name the same asset.
		rest := cask[index:]
		end := strings.Index(rest, "end")
		if !strings.Contains(rest[:end], asset) {
			t.Errorf("%s digest is paired with the wrong url", asset)
		}
	}
}

// A release missing an artifact must fail rather than emit a cask with a blank
// digest, which Homebrew would reject at install with no useful message.
func TestMissingAssetIsAnError(t *testing.T) {
	partial := "aaa1  captaincode_0.3.2_darwin_arm64.tar.gz\n"
	sums, err := parseChecksums(writeChecksums(t, partial))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := render("v0.3.2", sums); err == nil {
		t.Fatal("a partial release produced a cask")
	}
}

// Without this stanza every macOS install stops at Gatekeeper.
func TestCaskClearsQuarantineOnInstall(t *testing.T) {
	sums, _ := parseChecksums(writeChecksums(t, fourAssets))
	cask, err := render("v0.3.2", sums)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cask, "com.apple.quarantine") {
		t.Fatal("the cask does not clear the quarantine attribute")
	}
	// It must not be allowed to fail an install, since xattr is macOS-only.
	if !strings.Contains(cask, "must_succeed: false") {
		t.Fatal("the postflight can fail an install on Linux")
	}
}

// The cask is Ruby, and a malformed one is rejected when the tap is refreshed
// rather than when the binary is installed.
//
// Counting "do" against "end" was the first attempt and it is wrong: the blocks
// nest, so a flat count only balances when the nesting happens to be uniform —
// and it did not catch the real defect, which was on_macos and on_arm written
// flat instead of nested. The file is parsed instead.
//
// This uses the Ruby that ships with Homebrew rather than the system one, since
// the cask is evaluated by Homebrew's Ruby and a different major version can
// accept what it rejects.
//
// brew ruby has no -c, so the file is compiled instead. That is the check that
// matters: it rejects the flat on_macos / on_arm form this generator once
// produced, which a token-level check would have passed.
func TestCaskIsValidRuby(t *testing.T) {
	brew := os.Getenv("HOMEBREW_BIN")
	if brew == "" {
		t.Skip("set HOMEBREW_BIN to the brew executable")
	}
	sums, _ := parseChecksums(writeChecksums(t, fourAssets))
	cask, err := render("v0.3.2", sums)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "captaincode.rb")
	if err := os.WriteFile(path, []byte(cask), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `RubyVM::InstructionSequence.compile_file(ARGV[0]); puts("SYNTAX OK")`
	out, err := exec.Command(brew, "ruby", "-e", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("the cask is not valid Ruby: %v\n%s\n%s", err, out, cask)
	}
}

// Without Homebrew the structural checks still catch the gross failures.
func TestCaskHasTheExpectedStructure(t *testing.T) {
	sums, _ := parseChecksums(writeChecksums(t, fourAssets))
	cask, err := render("v0.3.2", sums)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cask, `cask "captaincode" do`) {
		t.Error("the cask does not open correctly")
	}
	if !strings.HasSuffix(strings.TrimSpace(cask), "end") {
		t.Error("the cask does not close correctly")
	}
	// The platform blocks must nest: on_macos do, then on_arm do inside it.
	// Written flat, the file parses as far as the first "do" and then fails.
	for _, want := range []string{
		"  on_macos do\n", "  on_linux do\n",
		"    on_arm do\n", "    on_intel do\n",
		`binary "captaincode"`, `version "0.3.2"`,
	} {
		if !strings.Contains(cask, want) {
			t.Errorf("the cask is missing %q", want)
		}
	}
	if strings.Contains(cask, "on_macos\n") {
		t.Error("the platform blocks are not nested")
	}
}

// The v prefix is stripped from the version but kept in the URL, which is where
// a mistake produces a cask that installs the wrong release.
func TestTagPrefixIsHandledOnBothSides(t *testing.T) {
	sums, _ := parseChecksums(writeChecksums(t, fourAssets))
	cask, err := render("v0.3.2", sums)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cask, `version "0.3.2"`) {
		t.Error("the version kept the v prefix")
	}
	if !strings.Contains(cask, "download/v0.3.2/") {
		t.Error("the download URL lost the v prefix")
	}
}

func TestEmptyChecksumsIsAnError(t *testing.T) {
	if _, err := parseChecksums(writeChecksums(t, "\n\n")); err == nil {
		t.Fatal("an empty checksums file was accepted")
	}
}

func TestMissingFileIsAnError(t *testing.T) {
	if _, err := parseChecksums(filepath.Join(t.TempDir(), "absent.txt")); err == nil {
		t.Fatal("a missing checksums file was accepted")
	}
}
