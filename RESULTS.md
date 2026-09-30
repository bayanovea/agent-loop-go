# Results — hand-written agent loop

A manual tool-use loop against the Anthropic Messages API: no `BetaToolRunner`, no framework. Two tools (`list_tasks`, `log_time`) over an in-memory slice. The loop assembles the conversation, sends it, executes whatever the model asks for, appends `tool_result` blocks, and repeats until `stop_reason` says otherwise.

Run: `go run .` (credentials come from an `ant auth login` profile — no API key needed).

## Measured run

Model `claude-haiku-4-5`, thinking disabled, 4 turns, prompt forcing two sequential `log_time` calls.

| Turn | Messages sent | Input | Output | `stop_reason` |
|---|---|---|---|---|
| 1 | 1 | 749 | 73 | `tool_use` |
| 2 | 3 | 849 | 118 | `tool_use` |
| 3 | 5 | 984 | 129 | `tool_use` |
| 4 | 7 | 1 130 | 72 | `end_turn` |
| | **Total** | **3 712** | **392** | |

Cost at Haiku 4.5 list price ($1 / $5 per MTok): **$0.00567 per run.**

## Where the pre-registered estimate broke

The estimate written before building was 4 300 input / 1 020 output over 5 turns. Against 3 712 / 392 the total input looks close — 14% off — but that number is a coincidence, and that is the interesting part.

| | Estimated | Actual | Error |
|---|---|---|---|
| Turn-1 base (system + 2 schemas + prompt) | 400 | 749 | **−87%** |
| Growth per turn | ~230 | ~127 | **+81%** |
| Output total | 1 020 | 392 | **+160%** |

Two large errors in opposite directions cancelled. The fixed base was nearly twice what was assumed — tool definitions with per-parameter descriptions are much heavier than they look — while each round trip added far less, because the `tool_result` payloads here are one-line JSON objects (~40 tokens), not the 150 assumed. Output was overestimated for a simpler reason: the estimate budgeted adaptive thinking, and thinking is commented out in this build, so there are no thinking tokens at all.

**A total that lands near the mark while every component is wrong is not an estimate, it is luck.** Checking only the bottom line would have confirmed a model that is wrong in both directions.

## The re-send multiplier, observed

Final-turn input is 1 130 tokens — that is the entire conversation plus tools plus system, the largest single payload. Total billed input across the run is 3 712. **Ratio 3.3×**: the same content is paid for repeatedly because the full history is re-sent every turn. Growth is quadratic in turns, so this is the lever prompt caching pulls in a real agent. At this scale, with tiny tool results, the fixed base dominates instead — the multiplier matters once results get large (file contents, HTML, dumps).

## Where to fix the double-count in `log_time`

`log_time` adds a delta, so it is not idempotent: two identical calls log the time twice. The fix belongs in the tool, not in the model and not in the loop — the model cannot be trusted not to repeat itself, and the transport is entitled to retry.

There are two distinct retries and they need different answers:

1. **Harness or transport retry** — the same request is replayed, so `tool_use.id` is unchanged. Pass that id into `logTime`, keep a set of applied ids, and on a repeat return the current state instead of applying again. The id is the natural idempotency key: it is unique per model request and already travels with the call.
2. **The model re-asking** — it never saw the result and issues a genuinely new call, with a **new** `tool_use.id`. The key does not help here, because nothing marks the two calls as the same intent. The fix is to make the operation declarative: `set_logged_time(task_id, total_seconds)` instead of `log_time(task_id, delta)`. Setting an absolute value is idempotent by construction, and the invalid state stops being representable.

Same lesson as the `swap_shift` exercise, inverted: that tool was dangerous because it was its own inverse, this one because it accumulates. Both are only safe once the *schema* makes the wrong call impossible rather than the handler catching it afterwards.

## Fixed during review

- **`log_time` never validated `seconds`.** The schema advertises `"minimum": 1`, but the API does not enforce it without `strict: true` — a schema is a hint to the model, not validation. `{"seconds": -3600}` silently subtracted. The handler is the trust boundary; `tool_use.input` is untrusted input, like an HTTP body.
- **Error messages made self-correcting.** Failures now carry every valid id and title (`Available tasks: 1="database migration", ...`), so the model can choose correctly or tell the user the task does not exist, rather than retrying blind until `maxTurns`. Removed a duplicated sentence, two `for for` typos, and the `tool_use_id` that was being echoed into error text — the model already has it.
- Removed a duplicate token-debug line; added a running total and per-run cost; `go mod tidy`.

## Known gaps

- `comment` is accepted by the schema and filled by the model, then discarded by the handler.
- No `sync.Mutex` on `tasks`. Correct today because the loop executes tool calls sequentially; executing parallel `tool_use` blocks in goroutines would race on `tasks[i].Time += seconds`.
- Thinking and `effort` are wired but commented out.
