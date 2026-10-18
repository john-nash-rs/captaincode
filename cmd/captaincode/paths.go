package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// defaultDatabasePath derives a database path from the workspace it serves.
//
// The database records the workspace it belongs to and refuses to open for any
// other one. A single shared default therefore works for exactly one project
// and fails for every project after it. Deriving the name from the resolved
// workspace path makes the default usable for any number of projects while
// staying deterministic: the same workspace always maps to the same file, so a
// run can be resumed or completed without repeating --db.
//
// The readable prefix is for humans listing the directory; the hash is what
// makes it unique. Two directories named "api" in different parents must not
// collide, and the filesystem root must not produce an empty name.
func defaultDatabasePath(workspace string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(workspace))
	slug := slugify(filepath.Base(filepath.Clean(workspace)))
	if slug == "" {
		slug = "workspace"
	}
	name := fmt.Sprintf("%s-%s.db", slug, hex.EncodeToString(sum[:6]))
	return filepath.Join(base, "captaincode", name), nil
}

// slugify keeps the readable part of a filename safe across filesystems: no
// separators, no shell metacharacters, no leading dot that would hide it.
func slugify(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune('-')
		}
	}
	// Collapse runs so long names stay readable and bounded.
	cleaned := strings.Trim(b.String(), "-")
	for strings.Contains(cleaned, "--") {
		cleaned = strings.ReplaceAll(cleaned, "--", "-")
	}
	if len(cleaned) > 40 {
		cleaned = strings.Trim(cleaned[:40], "-")
	}
	return cleaned
}

// isTerminal reports whether a file is attached to a terminal. The interface
// needs one on both ends; without it Bubble Tea fails deep inside its own setup
// with a message that does not name the cause or the way out.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// errPermission wraps a filesystem failure with the directory it concerns, so
// the operator is told what to fix instead of being shown a bare errno.
func errPermission(path string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("cannot write %s\n  fix: sudo chown -R $USER %s", path, path)
	}
	return fmt.Errorf("cannot write %s: %w", path, err)
}

// valueFlags are the flags that consume the following argument. Boolean flags
// do not, which is what makes reordering argv safe.
var valueFlags = map[string]bool{
	"workspace": true,
	"db":        true,
	"config":    true,
	"model":     true,
	"provider":  true,
	"question":  true,
	"resume":    true,
}

// permuteArgs moves flags ahead of positional arguments.
//
// The standard flag package stops parsing at the first non-flag argument, so
// "captaincode . --ide=false" would treat "--ide=false" as a project path. That
// is the order people naturally type, and silently reading a flag as a
// directory is a bad failure. Moving the flags to the front first makes the
// order irrelevant.
func permuteArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if valueFlags[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// flagName extracts the bare flag name from an argument such as "-db" or
// "--db=/tmp/x.db".
func flagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	return name
}
