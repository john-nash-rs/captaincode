// Command mkcask writes the Homebrew cask for a published release.
//
// It exists because GoReleaser's homebrew section either pushes to the tap
// itself or, with --skip, does not produce a cask at all. There was no way to
// generate one without also pushing it, which is what tied a correctly
// published release to the availability of a tap token.
//
// The cask is derived from the release's own checksums.txt, which is the same
// artefact Homebrew verifies against. Reading it back rather than recomputing
// means a digest can never disagree with what was published.
//
//	mkcask -tag v0.3.2 -checksums checksums.txt -out CaptainCode.rb
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

// caskTemplate is written verbatim apart from the target blocks.
//
// The postflight is the reason this file exists in its own right: Homebrew
// strips the quarantine attribute from an .app bundle but not from a cask that
// stages a bare binary, so every macOS install stopped at Gatekeeper with
// "Apple could not verify captaincode is free of malware". must_succeed is
// false because the attribute does not exist on Linux and an install must not
// fail over a macOS-only concern.
const caskTemplate = `cask "captaincode" do
  postflight do
    system_command "/usr/bin/xattr",
                  args: ["-dr", "com.apple.quarantine", "#{staged_path}/captaincode"],
                  must_succeed: false
  end

  version %q
%s
  name "captaincode"
  desc "A single-user coding-agent harness"
  homepage "https://github.com/john-nash-rs/captaincode"

  livecheck do
    skip "Auto-generated on release."
  end

  binary "captaincode"

  # No zap stanza required
end
`

// target is one published platform.
type target struct {
	OS   string // goreleaser name, e.g. darwin
	Arch string // amd64 or arm64
	Sha  string
	URL  string
}

// renderPlatforms emits the nested on_macos / on_linux blocks.
//
// The nesting matters: Homebrew's DSL treats on_macos and on_arm as two
// separate blocks, and writing "on_macos on_arm do" produces a cask that fails
// to parse at tap time rather than one that merely looks wrong.
func renderPlatforms(targets []target) (string, error) {
	platforms := []struct {
		keyword string
		os      string
	}{
		{"on_macos", "darwin"},
		{"on_linux", "linux"},
	}
	arms := []struct {
		keyword string
		arch    string
	}{
		{"on_arm", "arm64"},
		{"on_intel", "amd64"},
	}

	var b strings.Builder
	for _, p := range platforms {
		fmt.Fprintf(&b, "  %s do\n", p.keyword)
		for _, a := range arms {
			var found *target
			for i := range targets {
				if targets[i].OS == p.os && targets[i].Arch == a.arch {
					found = &targets[i]
					break
				}
			}
			if found == nil {
				return "", fmt.Errorf("no asset for %s/%s", p.os, a.arch)
			}
			fmt.Fprintf(&b, "    %s do\n      sha256 %q\n      url %q\n    end\n",
				a.keyword, found.Sha, found.URL)
		}
		b.WriteString("  end\n")
	}
	return b.String(), nil
}

func main() {
	tag := flag.String("tag", "", "release tag including the v prefix")
	checksums := flag.String("checksums", "checksums.txt", "path to the release checksums file")
	out := flag.String("out", "-", "output path, or - for stdout")
	flag.Parse()

	if *tag == "" {
		fmt.Fprintln(os.Stderr, "mkcask: -tag is required")
		os.Exit(2)
	}
	sums, err := parseChecksums(*checksums)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkcask:", err)
		os.Exit(1)
	}

	cask, err := render(*tag, sums)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkcask:", err)
		os.Exit(1)
	}
	if *out == "-" {
		fmt.Print(cask)
		return
	}
	// The file is committed to a repository and may be read by others.
	if err := os.WriteFile(*out, []byte(cask), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mkcask:", err)
		os.Exit(1)
	}
}

// parseChecksums reads a GoReleaser checksums file into asset name to digest.
func parseChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sums := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		sums[fields[1]] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s contained no checksums", path)
	}
	return sums, nil
}

// render produces the cask for one tag.
func render(tag string, sums map[string]string) (string, error) {
	version := strings.TrimPrefix(tag, "v")

	platforms := []target{
		{OS: "darwin", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "linux", Arch: "amd64"},
	}
	for i := range platforms {
		asset := fmt.Sprintf("captaincode_%s_%s_%s.tar.gz", version, platforms[i].OS, platforms[i].Arch)
		sum, ok := sums[asset]
		if !ok {
			return "", fmt.Errorf("no checksum for %s", asset)
		}
		platforms[i].Sha = sum
		platforms[i].URL = fmt.Sprintf(
			"https://github.com/john-nash-rs/captaincode/releases/download/%s/%s", tag, asset)
	}

	blocks, err := renderPlatforms(platforms)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(caskTemplate, version, blocks), nil
}
