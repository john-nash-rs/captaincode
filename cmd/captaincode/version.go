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
func usage(flags *flag.FlagSet) {
	out := stderr()
	fmt.Fprint(out, `captaincode - a single-user coding-agent harness

Usage:
  captaincode [DIR] [flags]        open the interface on DIR (default)
  captaincode [DIR] --ide=false   run one request and print the answer
  captaincode doctor              check runtime prerequisites
  captaincode version             print version information

The project directory is the first argument. It is never inferred, because
tools execute against it.

Flags:
`)
	flags.PrintDefaults()
	fmt.Fprint(out, `
Examples:
  captaincode .                              interface on the current project
  captaincode ~/src/myproject                interface on another project
  captaincode . --ide=false                  one-shot answer
  captaincode . --provider openrouter         with a hosted model
  captaincode . --db ~/.local/share/cc.db    choose the database explicitly

--tui was removed. The interface is now the default; use --ide=false for
the previous one-shot behaviour.

The database defaults to one file per workspace under the user config
directory, so separate projects do not collide. It must stay outside the
workspace. Start with "captaincode doctor" if tools fail to run;
CaptainCode refuses to execute tools without its sandbox rather than
falling back to running them unsandboxed.

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
