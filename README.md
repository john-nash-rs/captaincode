# CaptainCode

A single-user coding-agent harness in Go. CaptainCode wraps a language model in
the software required to use one safely: an explicit tool contract, a bounded
agent loop, filesystem sandboxing, durable run state, and a terminal interface
that reports what the system is actually doing.

The project is built around a single idea: **a model's output is a request, not
an action.** Every guarantee CaptainCode makes — bounded execution, recorded
observations, recoverable runs, visible permissions — follows from keeping that
distinction explicit.

> **Status: pre-1.0.** This is a single-operator tool, not a hosted service. It
> is designed to be read, run, and audited. It is not hardened for
> multi-tenancy or adversarial use. See [Limitations](#limitations).

![CaptainCode terminal interface](docs/tui.svg)

## Why it exists

Most agent failures are not model failures. A model that writes a perfectly good
patch still causes damage when the harness cannot answer four questions:

| Question | CaptainCode's answer |
|---|---|
| What was actually executed? | Nothing happens until intent is committed to durable state |
| What did the tool actually observe? | Every tool result is recorded; claims are never inferred |
| What if the process died mid-write? | The run is **blocked** for reconciliation, never reported as successful |
| What is this tool allowed to touch? | The sandbox decides, and there is no unsandboxed fallback |

## Design

Seven packages, each owning one responsibility:

| Package | Responsibility |
|---|---|
| `core` | The agent loop, budgets, context selection, run phases |
| `toolbox` | Tool contracts: validation, path containment, stale-edit detection |
| `sandbox` | OS-enforced isolation via `sandbox-exec` (macOS) or `bubblewrap` (Linux) |
| `storage` | SQLite snapshots and an event index with atomic commit |
| `provider` | OpenRouter adapter with an offline deterministic fixture |
| `keyring` | Credential resolution and private storage |
| `ui` | Bubble Tea terminal interface |

Two principles run through all of them:

**Deny by default.** Writes and shell commands are disabled. The sandbox refuses
to start rather than silently running tools without isolation. A failed setup is
an error, never a quiet downgrade to something less safe.

**Preserve uncertainty.** When an operation's outcome is unknown, CaptainCode
records it as unknown. A lost HTTP reply after a commit does not become a retry;
it becomes a blocked run that requires a human.

## Installation

### Homebrew

```sh
brew tap john-nash-rs/tap && brew install captaincode
```

Or without tapping first:

```sh
brew install john-nash-rs/tap/captaincode
```

### Shell installer

```sh
curl -fsSL https://YOUR-DOMAIN/install.sh | sh
```

> **`YOUR-DOMAIN` is a placeholder.** No domain has been configured yet, so
> that URL does not resolve. Until one is, use the repository URL:
>
> ```sh
> curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh
> ```

The installer downloads the archive for your platform, verifies its SHA-256
against the release's `checksums.txt`, and only then installs it into
`~/.local/bin`. No privileges are required.

```sh
sh install.sh --version v0.1.0        # pin a specific version
sh install.sh --dir ~/bin            # choose the install directory
sh install.sh --help
```

> **Risk of piping a remote script into a shell.** This executes whatever the
> remote host serves, with your privileges, before you read any of it. Prefer
> downloading and reading it first:
>
> ```sh
> curl -fsSLO https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh
> less install.sh
> sh install.sh
> ```

### Manual download

Grab the archive and `checksums.txt` from
[GitHub Releases](https://github.com/john-nash-rs/captaincode/releases):

```sh
VERSION=v0.1.0
curl -fsSLO "https://github.com/john-nash-rs/captaincode/releases/download/$VERSION/captaincode_${VERSION#v}_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz"
curl -fsSLO "https://github.com/john-nash-rs/captaincode/releases/download/$VERSION/checksums.txt"
shasum -a 256 -c checksums.txt
tar -xzf captaincode_*.tar.gz
sudo mv captaincode /usr/local/bin/   # or ~/.local/bin, no sudo needed
```

**Verify the checksum before running anything.** A mismatch means the download
is corrupt or was tampered with.

### Supported platforms

| OS | Architectures |
|---|---|
| macOS | `arm64` (Apple Silicon), `amd64` (Intel) |
| Linux | `amd64` (x86-64), `arm64` (aarch64) |

Linux requires kernel **3.10+**. Windows is not supported.

### Verify the installation

```sh
captaincode version
captaincode --version
captaincode doctor
captaincode --help
```

`doctor` checks that the sandbox backend this platform needs is present. It
reports and never repairs: CaptainCode refuses to run tools without isolation
rather than falling back to running them unsandboxed.

### Sandbox prerequisites

Installing the binary does **not** install its sandbox dependency.

| Platform | Backend | Action |
|---|---|---|
| macOS | `/usr/bin/sandbox-exec` | Ships with the OS. Deprecated by Apple; host or MDM policy may remove it. |
| Linux | `bubblewrap` (`bwrap`) | `sudo apt-get install bubblewrap`, plus unprivileged user namespaces |

If `doctor` reports a missing sandbox, tools will not run. That is the
intended behaviour, not a bug.

### Upgrade

```sh
brew upgrade captaincode                          # Homebrew
curl -fsSL https://YOUR-DOMAIN/install.sh | sh    # shell installer
```

Or download the new release manually. Check [releases](https://github.com/john-nash-rs/captaincode/releases)
for what changed.

### Uninstall

```sh
brew uninstall captaincode                        # Homebrew
rm ~/.local/bin/captaincode                       # shell installer
```

Optional state also lives outside the binary and is **not** removed by
uninstalling:

```sh
rm ~/.config/captaincode/sessions.db     # session database
rm ~/.config/captaincode/openrouter.key  # stored credential
```

Remove the credential file if you are uninstalling for security reasons.

### PATH troubleshooting

`command not found: captaincode` after installing means the install directory
is not on `PATH`. The installer prints the exact line to add. For `~/.local/bin`:

```sh
sh   -c 'echo "export PATH=\"$HOME/.local/bin:$PATH\"" >> ~/.profile'
zsh  -c 'echo "export PATH=\"$HOME/.local/bin:$PATH\"" >> ~/.zshrc'
bash -c 'echo "export PATH=\"$HOME/.local/bin:$PATH\"" >> ~/.bashrc'
```

Then open a new shell. Confirm with `captaincode version`.

### Build from source

```sh
git clone https://github.com/john-nash-rs/captaincode.git
cd captaincode
go mod download
go test ./...
go build -o bin/captaincode ./cmd/captaincode
```

A binary built this way reports `captaincode dev` and says it is an unreleased
build. Release builds embed their version via the linker.

## Quick start

```sh
captaincode --workspace ./testdata/project --db /tmp/captaincode-demo.db
```

```
The project version is 2.5.0 (package.json).
Run: 526e845c47c9161ecafe395fe6776714
```

The database must live **outside** the workspace. Each database is bound to one
canonical project path and refuses another; use a fresh `--db` per project.

### The terminal interface

```sh
./bin/captaincode --tui --workspace ./testdata/project --db /tmp/captaincode.db
```

Type a question and press Enter. Type while a run is active and it queues.
**Escape** cancels the underlying request, not just the display. `Ctrl+C`
exits once the current run has recorded its outcome.

The footer always reports the connected provider, model, and permission grants
in force. It states what is connected — it never infers it.

### Commands

Prefix a line with `/` to run an interface command. These are handled inside the
interface and are never sent to a model. **Tab** completes to the highlighted
suggestion.

| Command | Effect |
|---|---|
| `/openrouter` | Report provider, model, credential, and keyfile path |
| `/openrouter api-key` | Store a provider credential, with typed characters hidden |
| `/openrouter model <id>` | Store a model identifier |
| `/openrouter clear-key` | Remove the stored credential |
| `/openrouter path` | Print the keyfile path |

### Connecting a provider

A missing credential is **not** a startup error. Start the interface, run
`/openrouter api-key`, paste the key, and the next request uses it — no restart.

Credentials resolve per request, so rotating a key or changing a model takes
effect immediately.

```sh
./bin/captaincode --provider openrouter \
  --workspace ./testdata/project \
  --db /tmp/captaincode-live.db \
  --config ~/.captaincode.json
```

```json
{ "model": "openrouter/free", "allow_writes": false, "allow_commands": false }
```

`OPENROUTER_API_KEY` overrides the stored file for a single shell session.

**Where the credential lives.** `OPENROUTER_API_KEY` first, then
`<user-config-dir>/captaincode/openrouter.key` — a `0600` file inside a `0700`
directory. It is written through a temporary name so a crash cannot leave a
partial credential. It is never written into the configuration file, never
placed in the conversation transcript, and displayed as at most its final four
characters. `/openrouter path` prints the location so you can delete it.

This is a bearer secret protected by filesystem permissions alone. There is no
OS keychain integration, no encryption at rest, and no rotation policy.

Requests are sent to OpenRouter and its routed provider. **This can incur
charges**, and selected project content leaves your machine.

## Configuration

Only an explicit `--config` file overrides defaults; no project config is
auto-loaded. Unknown fields, out-of-range values, and trailing JSON are
rejected rather than ignored.

| Field | Default | Meaning |
|---|---|---|
| `max_turns` | `12` | Ceiling on model round-trips per run |
| `max_tool_calls` | `24` | Ceiling on tool invocations per run |
| `context_bytes` | `65536` | Serialized conversation bound |
| `output_bytes` | `8192` | Per-tool output cap |
| `tool_timeout_seconds` | `15` | Per-tool deadline |
| `run_timeout_seconds` | `180` | Whole-run deadline |
| `model` | — | Provider model identifier |
| `allow_writes` | `false` | Coarse grant for the exposed project |
| `allow_commands` | `false` | Coarse grant for the exposed project |

Enabling writes or commands is a **coarse project-wide grant, not a per-call
approval.** There is no interactive approval prompt. Use a disposable copy of
your project when experimenting.

The context bound is measured in serialized bytes, not provider tokens. Old
assistant and tool groups may be dropped, which loses evidence; there is no
summarization or retrieval layer.

## Recovery semantics

```sh
./bin/captaincode --workspace ./testdata/project --db /tmp/captaincode.db --resume RUN_ID
```

| Phase on restart | Behavior |
|---|---|
| `completed` | Returns the saved answer; no new model or tool events |
| `cancelled`, `failed`, `blocked` | Terminal; refuses to resume |
| interrupted **read** | Safe to repeat; re-executes |
| interrupted **mutation**, outcome known | Recorded |
| interrupted **mutation**, outcome unknown | **Blocked** for operator reconciliation |

There is no automatic reconciliation service. **A checkpoint cannot provide
exactly-once external effects.**

## Learning checkpoints

The repository contains runnable chapter entry points:

```sh
./checkpoint 02      # standalone minimal agent loop
./checkpoint 10      # idempotency and lost replies
./checkpoint 18      # the assembled harness
```

See [the checkpoint map](checkpoints/README.md). Retry and concurrent scheduling
are separate labs under `labs/`; the core loop is serial and does not
automatically retry provider requests.

## Verification

```sh
go mod verify
go vet ./...
gofmt -l .
go test -race -count=1 ./...
CAPTAINCODE_SANDBOX_TEST=1 go test -count=1 ./sandbox
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The sandbox probes are opt-in because they launch real processes and require
loopback listeners. They assert both permitted and forbidden operations. If
isolation cannot be established, tool execution fails — there is no fallback.

`govulncheck` queries public vulnerability data at run time. It is dated
evidence, not a security proof.

## Limitations

Read [LIMITATIONS.md](LIMITATIONS.md) before exposing a sensitive workspace. The
material ones:

- Core execution is **serial**; bounded retries and a concurrent scheduler exist
  only as labs.
- Permissions are **coarse grants**, not per-call approvals.
- macOS isolation uses a **deprecated** Apple interface.
- No resource quotas for CPU, memory, or disk; no seccomp policy.
- Configuration is not fully snapshotted; a database is bound to one workspace
  **path**, not an immutable image.
- Unknown mutations **block** for reconciliation.
- The terminal interface has no live-token rendering, no approval dialog, and no
  conversation steering.

## License

MIT — see [LICENSE](LICENSE). Dependency licenses are inventoried in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

CaptainCode is not affiliated with or endorsed by OpenRouter, Anthropic, or any
model provider.

## Maintaining a release

Releases are driven entirely by GitHub Actions. Nothing is published by hand.

**1. Configure the token** (once). The cask is pushed to the separate
`john-nash-rs/homebrew-tap` repository, so it needs a token the workflow's own
`GITHUB_TOKEN` cannot provide. The tap itself already exists and is
initialised — see [homebrew-tap/README.md](homebrew-tap/README.md).

```sh
gh secret set HOMEBREW_TAP_TOKEN --repo john-nash-ns/captaincode
```

Use a fine-grained token with contents read-write on `homebrew-tap` only. It
cannot be named `GITHUB_PERSONAL_AUTH_TOKEN` as some GoReleaser guides
suggest — GitHub reserves the `GITHUB_` prefix and rejects the name.

**2. Tag and push.**

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow runs tests, validates the GoReleaser config, builds all four
targets, publishes the archives with `checksums.txt`, and updates the cask.

**Verifying without publishing.** Build the exact artifacts locally:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
ls dist/
sh -cd dist && shasum -a 256 -c checksums.txt
```

**Uninstalling a bad release.** GoHub Releases are immutable once published by
default; delete the tag and the release, then publish a corrected version.