# Scope and limitations

This is a teaching implementation for a cooperative single user, not a hardened multi-tenant service.

- Core tools execute serially. The independent-call scheduler and bounded retry helper are separate tested labs. Provider retries are disabled.
- Fixture inference solves only a `package.json` version query. It is not a general-purpose planner.
- Config permissions are coarse. No interactive approval protocol, command allowlist, or policy service exists.
- Sandbox dependencies must work on the host. macOS uses deprecated sandbox-exec. Linux uses bubblewrap and requires suitable namespace policy. Check the dated verification report for actual platform tests.
- Platform library directories remain readable. The sandbox is not an empty filesystem. Selected top-level sensitive names are denied/masked, but there is no general secret detection, recursive secret exclusion, or protection against all hard-link and alias configurations. Expose a sanitized project copy.
- Arbitrary enabled commands can execute shells and alter the allowed workspace. There is no resource quota for CPU/memory/disk and no seccomp policy in the Linux wrapper. Process-group cancellation covers ordinary descendants, not every adversarial process escape, especially on macOS.
- Write digest checking is not atomic compare-and-swap with independent editors. Replacement mode is 0600; other metadata is not preserved. Directory fsync is absent. Stale temporary files cause failure and need deliberate inspection.
- Unknown mutations block recovery. No automatic reconciliation service exists. A checkpoint cannot provide exactly-once external effects. The order backend is an isolated single-tenant lab without real transactions, authentication, or an agent tool integration.
- SQLite stores full latest snapshots plus an event index, not a replayable event log. It is unencrypted. Snapshot size grows with history; no retention or disk-quota manager exists. SQLite sidecar files also contain sensitive data. Treat the storage directory as private and use an appropriate backup method.
- Current config applies on resume; a complete effective-config snapshot is not stored. A database is bound to one canonical workspace path, not an immutable workspace image.
- Context is bounded in serialized bytes, not provider tokens. Old assistant/tool groups may be removed, losing useful evidence. There is no summary model or semantic retrieval layer.
- HTTP response data is capped at 2 MiB. Streaming requires normal completion, valid JSON, stable call IDs, and supported metadata. Streamed reasoning metadata is rejected; live-provider compatibility remains optional and unverified until explicitly tested.
- UI shows coarse progress and final text in a framed editor with a footer reporting the resolved provider, model, and permission grants. That footer displays state the caller passed; it is not an approval mechanism and grants nothing. No live-token rendering, approval dialog, scrollback navigation, multi-line input editing, or conversation steering. Queued prompts are independent runs.
- UI color is disabled for `NO_COLOR`, `TERM=dumb`, and non-TTY stderr. It is not checked against terminal capability profiles, so a color-limited terminal may still receive truecolor escape codes.
- Tests use local HTTP servers and temporary files. OS enforcement probes require explicit opt-in. A green suite does not prove security or task accuracy for arbitrary live models.
- No independent technical review, professional copyedit, beta-reader build validation, hosted CI run, or Kindle Previewer test is implied by this source tree.
