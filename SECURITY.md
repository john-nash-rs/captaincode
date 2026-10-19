# Security Policy

## Reporting a vulnerability

Please **do not open a public issue**. Email the maintainer at the address on the
GitHub profile for `john-nash-rs`.

Include:

- the version (`captaincode version`) and platform
- a reproduction, ideally the smallest directory and prompt that triggers it
- what you expected and what happened instead

You will get an acknowledgement. Fixes are released as a patch version and
credited unless you would rather not be named.

## What CaptainCode is for

It is a single-operator tool for one person on one machine, against one project
at a time. Its guarantees are about what the agent is *able* to do, not about
resisting an attacker who already controls your account.

Threat model, concretely:

| In scope | Out of scope |
|---|---|
| A model proposing a write or command that goes beyond the operator's grant | Multi-tenancy — this is single-user by design |
| A tool escaping the workspace via `..`, an absolute path, or a symlink | An attacker with shell access as you, who can read `~/.captaincode.json` anyway |
| Silent failure — a mutation whose outcome is unknown being reported as successful | Malicious content *inside* a project you chose to open |
| A sandbox that fails to start and tools running anyway | Denial of service against your own machine |
| Checksum mismatch on a downloaded release | Supply-chain compromise of this repository's CI |

A prompt-injection payload committed into a repository you point CaptainCode at
**is in scope** to the extent that the sandbox, path containment, and grant
checks hold. It is not in scope as a claim that the tool will never follow a bad
instruction — nothing at this layer can promise that, and a tool that claims to
is misleading you.

## Known weaknesses

Stated plainly, because a security policy that implies otherwise is worse than
none. The full list is in [LIMITATIONS.md](LIMITATIONS.md).

- **The credential is a bearer secret protected by filesystem permissions
  only.** A `0600` file in a `0700` directory. No OS keychain, no encryption at
  rest, no rotation policy. `/openrouter clear-key` deletes it.
- **macOS isolation uses `sandbox-exec`, which Apple has deprecated.** It works
  today. It may stop working on a future macOS, and host or MDM policy can
  already remove it. `doctor` reports its absence as a failure.
- **There are no resource quotas.** No CPU, memory, or disk limits, and no
  seccomp policy. A runaway command is bounded by a timeout, not by a cgroup.
- **Permission grants are coarse and project-wide.** There is no per-call
  approval prompt, so there is no prompt to approve.
- **No Windows isolation backend exists**, which is why no Windows binary is
  published.
- **A blocked run waits for you.** When a mutation's outcome is unknown the run
  blocks rather than retrying. This is a safety property, but it means
  reconciliation is manual and cannot be skipped.

## Hardening for shared or sensitive workspaces

- Work on a copy. The database must live outside the workspace, but nothing stops
  the *workspace* being one you care about.
- Keep `allow_writes` and `allow_commands` off unless you mean it, and treat
  turning them on as the point of no return for that directory.
- Run `govulncheck ./...` before trusting a build; it is dated evidence, not a
  proof.
- Verify release checksums if you install manually — see
  [Manual download](README.md#manual-download).