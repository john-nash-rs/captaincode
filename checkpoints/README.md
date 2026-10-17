# Runnable chapter checkpoints

These entries share the final tested source. Chapter 02 also contains a standalone minimal loop. Later entries are focused executable checkpoints, not independent historical copies. Read earlier chapters for prerequisites. Run commands from the companion root.

| Chapter | Source | Run | Observation |
|---|---|---|---|
| 01: Model and system | [checkpoints/02-loop/loop.go](../checkpoints/02-loop/loop.go) | `./checkpoint 01` | Identify which component owns each message. |
| 02: Agent loop | [checkpoints/02-loop/loop.go](../checkpoints/02-loop/loop.go) | `./checkpoint 02` | Stop only when no calls remain or a control condition ends the run. |
| 03: Tool contracts | [toolbox/registry.go](../toolbox/registry.go) | `./checkpoint 03` | Reject invalid arguments before observing an effect. |
| 04: Bounded retries | [labs/retry/retry.go](../labs/retry/retry.go) | `./checkpoint 04` | Count attempts and cancel during backoff. |
| 05: Deadlines | [core/engine.go](../core/engine.go) | `./checkpoint 05` | Follow cancellation from context to tool execution. |
| 06: Context | [core/context.go](../core/context.go) | `./checkpoint 06` | Preserve instruction and call/result groups under a byte bound. |
| 07: Permissions | [evals/eval_test.go](../evals/eval_test.go) | `./checkpoint 07` | Recover a planned write under revoked permission. |
| 08: Sandbox | [sandbox/worker.go](../sandbox/worker.go) | `./checkpoint 08` | Run positive and negative OS boundary probes; requires a supported host. |
| 09: Streaming | [provider/stream.go](../provider/stream.go) | `./checkpoint 09` | Assemble complete calls and reject incomplete streams. |
| 10: Idempotency | [labs/orders/timeout_test.go](../labs/orders/timeout_test.go) | `./checkpoint 10` | Lose a reply after commit and reuse the same operation key. |
| 11: Checkpointing | [storage/sqlite.go](../storage/sqlite.go) | `./checkpoint 11` | Roll back snapshot and event together on an injected failure. |
| 12: Recovery | [core/engine_test.go](../core/engine_test.go) | `./checkpoint 12` | Block an uncertain mutation even if the terminal-state save failed. |
| 13: Concurrency | [labs/scheduler/scheduler.go](../labs/scheduler/scheduler.go) | `./checkpoint 13` | Bound independent work and correlate results by ID. |
| 14: Configuration | [config/config.go](../config/config.go) | `./checkpoint 14` | Reject unknown settings and apply current policy. |
| 15: Evaluation | [evals/eval_test.go](../evals/eval_test.go) | `./checkpoint 15` | Use fresh workspaces and inspect behavior as well as answers. |
| 16: Terminal UI | [ui/ui.go](../ui/ui.go) | `./checkpoint 16` | Queue input, ignore stale events, and propagate cancellation. |
| 17: Edits and commands | [toolbox/registry.go](../toolbox/registry.go) | `./checkpoint 17` | Detect stale edits and preserve command uncertainty. |
| 18: Assembled harness | [cmd/captaincode/main.go](../cmd/captaincode/main.go) | `./checkpoint 18` | Build the CLI after the complete deterministic suite passes. |
