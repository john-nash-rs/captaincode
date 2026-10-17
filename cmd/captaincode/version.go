package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
)

// printVersion writes the release identity.
//
// A binary built straight from a working tree reports "dev", which is the
// point: an unreleased build should never be mistaken for a tagged release.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "captaincode %s\n", version)
	fmt.Fprintf(w, "  commit:   %s\n", commit)
	fmt.Fprintf(w, "  go:       %s\n", runtime.Version())
	fmt.Fprintf(w, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if version == "dev" {
		fmt.Fprintf(w, "\nThis is an unreleased development build.\n")
	}
}

func stdin() io.Reader { return os.Stdin }

func stdout() io.Writer { return os.Stdout }

func stderr() io.Writer { return os.Stderr }

// usage prints the top-level help. The standard flag package is used
// deliberately; a hand-rolled parser would add a dependency for no gain.
func usage() {
	out := stderr()
	fmt.Fprint(out, `captaincode - a single-user coding-agent harness

Usage:
  captaincode --workspace DIR [flags]     run one request
  captaincode --tui --workspace DIR       open the terminal interface
  captaincode doctor                     check runtime prerequisites
  captaincode version                    print version information

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(out, `
Examples:
  captaincode --workspace ./myproject --db ~/.local/share/captaincode/p.db
  captaincode --tui --workspace ./myproject --db ~/.local/share/captaincode/p.db
  captaincode --provider openrouter --workspace ./p --db /tmp/p.db

The database must be outside the workspace. Start with "captaincode doctor"
if tools fail to run; CaptainCode refuses to execute tools without its
sandbox rather than falling back to running them unsandboxed.

Environment:
  OPENROUTER_API_KEY   credential override; takes precedence over the stored key
  NO_COLOR             disable colour in the terminal interface

Docs: https://github.com/john-nash-rs/captaincode
`)
}

// newFlagSet returns a flag set that reports errors and usage consistently
// across subcommands, rather than printing the default package usage.
func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr())
	return set
}
