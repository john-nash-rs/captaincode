# CaptainCode companion

A small, single-user coding-agent harness for **Build an AI Coding Agent** by Harsh Vardhan. Concepts are language-independent; this implementation uses Go. Review draft, 17 October 2026.

## Start offline

Use **Go 1.26.9** (the minimum in go.mod) on macOS or Linux. Earlier authoring tests used 1.26.3; a vulnerability audit prompted the patch upgrade. The Go command may download the requested toolchain if automatic toolchain selection is enabled. Dependency download needs network access; the tests do not need a paid model account.

```sh
go version
go mod download
go test ./...
go build -o bin/captaincode ./cmd/captaincode
./bin/captaincode --workspace ./testdata/project --db /tmp/captaincode-demo.db
```

Expected final answer: `The project version is 2.5.0 (package.json).` A run ID and event metadata also appear. Use a fresh database path when exposing a different workspace. The store binds a database to one canonical project path and refuses another project.

The CLI always runs tools through the sandbox worker. Ordinary package tests also exercise the registry directly for deterministic contract tests; this is not an unrestricted CLI fallback.

## Platform requirements

- **macOS:** `/usr/bin/sandbox-exec` must exist and work. It is a deprecated Apple interface. The tested host and boundary probes are recorded in the book's verification report. Host or enterprise policy can prevent nested sandbox execution.
- **Linux:** install `bubblewrap` (`bwrap`) and allow the user namespace operations it requires. On Debian/Ubuntu, the distribution package is normally installed with `sudo apt-get install bubblewrap`. The wrapper uses platform libraries, an isolated network namespace, and a single project mount. No claim of Linux runtime verification follows from cross-compilation; see the report.
- Windows is unsupported by the native worker and database lock implementation.

Run real boundary probes explicitly:

```sh
CAPTAINCODE_SANDBOX_TEST=1 go test -count=1 ./sandbox
```

The network probe also requires `/usr/bin/curl`. Tests check allowed operations as well as forbidden ones. A failed isolation setup prevents tool execution; there is no unsandboxed fallback.

## Learning checkpoints

```sh
./checkpoint 02
./checkpoint 10
```

See [the checkpoint map](checkpoints/README.md). Chapter 2 is a standalone minimal implementation. Later checkpoints are focused executable tests and source-reading paths through the assembled harness, not duplicated historical snapshots. Retry and concurrent scheduling are separate labs; the CLI core remains serial and does not automatically retry provider requests.

## Optional live model

Copy `examples/read-only.json` to a private user configuration file. Add a `model` property containing a current OpenRouter model identifier with tool support. Set `OPENROUTER_API_KEY` in your shell through your normal secret-management process; do not put it in the project, config, command history, or a shared transcript.

```sh
./bin/captaincode --workspace ./testdata/project \
  --db /tmp/captaincode-live.db --config /path/to/private-config.json \
  --provider openrouter --question 'What is the project version?'
```

This sends selected project content to OpenRouter and its routed provider and can incur charges. No live provider call was required for authoring verification. `--stream` enables complete stream assembly; streamed reasoning metadata is explicitly unsupported and produces an error. Non-streaming preserves opaque reasoning details for round-tripping.

## Configuration and authority

No project config is auto-loaded. Only the explicit `--config` file overrides defaults. Unknown fields, invalid ranges, and trailing JSON are rejected. Secrets are not config fields. `allow_writes` and `allow_commands` default to false. Enabling them is a coarse grant for the exposed project, not a per-call approval. Use a disposable copy when experimenting.

The database must be outside the tool workspace. Default storage uses the OS user-config directory under `captaincode/sessions.db`; pass `--db` for each separate project. A process lock prevents two application instances from owning the same database. Locking does not coordinate external editors or aliases to the same database inode through hard links.

```sh
./bin/captaincode --workspace ./testdata/project --db /tmp/captaincode-demo.db --resume RUN_ID
./bin/captaincode --workspace ./testdata/project --db /tmp/captaincode-ui.db --tui
```

Replace `RUN_ID` with an actual saved identifier. Completed runs return their saved answer. Cancelled, failed, and blocked runs are terminal. Interrupted reads may be repeated; unknown mutations block for reconciliation. Current permissions apply on recovery. The UI queues independent new runs, not follow-up messages to an active conversation. Enter submits, Escape cancels, Ctrl+C requests cancellation and exits after completion.

## Verification

```sh
go mod verify
go vet ./...
go test -race -count=1 ./...
CAPTAINCODE_SANDBOX_TEST=1 go test -count=1 ./sandbox
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The last command downloads a scanner if absent and queries public vulnerability data. It is dated evidence, not a security proof. The CI workflow is ready to run when this directory becomes a repository root; merely including the file does not mean hosted CI has run. The action major-version tags should be pinned to reviewed commit SHAs before a public release.

Read [limitations](LIMITATIONS.md) before exposing sensitive workspaces. Public code licensing and the final module/repository name await the author's release decision; no public publication has occurred.
