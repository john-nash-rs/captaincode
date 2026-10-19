# Contributing to CaptainCode

Small project, one maintainer, no roadmap beyond making the guarantees in the
README true on more platforms. Issues and pull requests are both welcome; a clear
description of what you want to happen matters more than polish.

## Before you start

```sh
go test ./... && go vet ./... && gofmt -l .
```

If those are clean and your change keeps them clean, the review will be quick.

## What changes are welcome

**Most welcome**, because they close real gaps:

- **A Windows sandbox backend.** The database lock, process setup, and `doctor`
  reporting are already ported. The missing piece is isolation. This is the
  single highest-value contribution available.
- **A Linux sandbox backend that does not need `bubblewrap`.**
  `seccomp`-based, or user namespaces where `bwrap` is unavailable.
- **A concurrency scheduler.** The core loop is serial by design. There is a
  prototype in `labs/scheduler`; the work is making it correct enough to argue
  its way into `core`.
- **A benchmark.** Reproducible, with the machine and the exact command stated.
  A number without the method is not accepted.
- **Platforms beyond macOS and Linux**, and their sandbox backends.

**Also welcome:** bug reports with a reproduction, clearer failure messages,
documentation of something that confused you, and tests for behaviour that is
currently unpinned.

**Please open an issue before starting**, for anything touching `sandbox`,
`storage`, or the run-state machine. Those carry the project's central claims
and are easier to change with agreement than to revert afterwards.

## What will be declined

The README's design rests on one distinction: **a model's output is a request,
not an action.** Changes that blur it tend to be declined, and it is fairer to
tell you now than after you have written the code.

- **Running tools without isolation, with a warning instead of a refusal.**
  A degraded sandbox is the failure this project exists to prevent.
- **Inferring a tool's effect instead of recording it.** If the outcome is
  unknown, the run blocks. It does not become a guess.
- **Per-call approval prompts in place of coarse grants.** Not because they are
  a bad idea — they are a different tool, with a different threat model. Adding
  them to this one would leave neither coherent.
- **A provider other than OpenRouter in `provider/`.** The interface is small
  enough that this is genuinely a design constraint. Contributions that
  generalise the interface without adding a second implementation are welcome.

## Style

- Match the surrounding code. `gofmt` is not negotiable.
- Comments explain *why*, not *what*. Where a design choice has a non-obvious
  reason, the reason is usually already written down at the site — keep it
  there rather than in a commit message.
- British or American spelling are both fine; consistency within a file is not
  something to enforce in review.
- No new runtime dependencies without discussion. There are four today —
  Bubble Tea, Lip Gloss, the OpenRouter SDK, and a pure-Go SQLite — and the bar
  for a fifth is a good reason, not convenience.

## Tests

`go test ./...` runs offline and must stay that way. Tests that need a network or
a credential skip themselves rather than failing, so CI runs without secrets.

The sandbox probes are opt-in because they launch real processes:

```sh
CAPTAINCODE_SANDBOX_TEST=1 go test -count=1 ./sandbox
```

They assert permitted *and* forbidden operations. If isolation cannot be
established they fail, because a probe that passes without isolating anything
is worse than no probe.

`evals/journey_test.go` drives the real built binary through a user journey and
is the gate the release workflow depends on. If you change a user-visible flow,
it likely needs a case there.

## Reporting a security problem

Please do not open a public issue. Email the maintainer directly; the address is
on the GitHub profile. Include the version (`captaincode version`), the platform,
and a reproduction. You will get an acknowledgement.

## Releasing

Releases are entirely automated — see [Maintaining a
release](README.md#maintaining-a-release). Do not publish anything by hand.

## License

Contributions are accepted under the MIT license that covers this project.