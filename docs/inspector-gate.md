# Inspector Gate

By default, ralphex trusts the worker agent to mark its own tasks complete: the worker ticks the
plan's checkboxes and emits `<<<RALPHEX:ALL_TASKS_DONE>>>`, and ralphex believes it. The inspector
gate replaces that trust with a check. The worker **proposes** completion; a **separately
credentialed inspector** renders a verdict, and the parent ralphex process — not the worker — records
the result.

This is opt-in and disabled by default. Enable it with `--inspector-gate` or
`inspector_gate_enabled = true`.

## Threat model

The worker is a capable agent with shell access. Nothing stops it from ticking a checkbox it didn't
earn, or from claiming a task is done when it isn't. The gate closes that gap with two structural
guarantees:

1. **The parent records completion, on the inspector's word.** The worker is told not to touch
   checkboxes; the parent ticks them, and only after the inspector returns `done`. This is
   mechanism-enforced, not just instruction-enforced: task selection and the all-done decision come
   from the parent-controlled state store (SQLite), not the plan's checkboxes. A worker that ticks
   its own boxes gains nothing — the parent re-derives the next task from the store and resets any
   forged tick before re-running the task, so a task is "done" only when an inspector `done` verdict
   set its store status.
2. **The worker cannot forge a verdict.** The inspector runs as a separate subprocess (the configured
   external-review tool) that the parent invokes and whose stdout the parent parses. If you also scrub
   the inspector's credentials from the worker's environment (see [Credential isolation](#credential-isolation)),
   the worker cannot impersonate the inspector even if it tries.

The security comes from the orchestration — parent runs the inspector, parent parses the verdict,
parent checks the boxes — not from a secret token. The token scrubbing is defense in depth so a
compromised worker can't reach the inspector's credentials.

## How it works

With the gate enabled, the task phase runs **one task per worker invocation**:

1. The worker is told to implement exactly the next uncompleted task, commit it, and **not** tick any
   checkbox. When done it emits `<<<RALPHEX:PEASANT_IS_TIRED>>>`.
2. The parent captures the commit range for that task (`HEAD` before/after) and runs the inspector on
   the diff, giving it the task's **acceptance criteria** (its checklist) so it can judge the diff on
   both completeness (are the criteria met?) and scope (does the diff stay within this task?) rather
   than guessing from the diff alone. A criterion phrased as "ensure X" can be satisfied by an empty
   diff when X already holds.
3. The inspector returns one of three verdicts:
   - **`done`** — the parent ticks that task's checkboxes and advances to the next task.
   - **`reject | <reason>`** — the reason is appended to the task in the plan file as an
     `⚠️ INSPECTOR REJECTION` note. The same task re-runs with a fresh worker session that re-reads the
     plan and addresses the complaint. After `max_task_attempts` rejections, the task escalates to the
     oracle.
   - **`update | <reason>`** — the task itself looks malformed or infeasible; it escalates to the
     oracle immediately.
4. **Oracle** (escalation): the external tool proposes one literal find/replace edit to the plan's task
   spec. The change is shown to you for approval (`Yes`/`No`). On approval it is applied, the task's
   attempt count resets, and the loop retries the revised task. On decline, the run aborts and the plan
   is left untouched.

Per-task attempt counts and statuses are persisted in a SQLite store at
`.ralphex/inspector-state.db`, so the gate survives restarts.

The gate applies to the task phase of `--inspector-gate` runs (full and tasks-only modes). The
existing post-task code-review phases are unchanged.

## Quick setup

The inspector reuses ralphex's external-review tool. The recommended setup uses
[opencode](https://opencode.ai) with a model from a **different family than the worker** — a Claude
worker checked by a DeepSeek inspector means the two don't share blind spots.

```bash
OPENCODE_REVIEW_MODEL=deepseek/deepseek-v4-pro \
ralphex --inspector-gate \
  --external-review-tool=custom \
  --custom-review-script=scripts/opencode/opencode-review.sh \
  docs/plans/feature.md
```

Or persist it in config (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
inspector_gate_enabled = true
external_review_tool = custom
custom_review_script = /path/to/ralphex/scripts/opencode/opencode-review.sh
# max_task_attempts = 3
```

`OPENCODE_REVIEW_MODEL` is read by `opencode-review.sh`; set it in your shell or wrapper. Any external
tool works — `external_review_tool = codex` uses Codex as the inspector instead.

## Configuration

| Setting | CLI flag | Default | Meaning |
|---------|----------|---------|---------|
| `inspector_gate_enabled` | `--inspector-gate` | `false` | Enable the per-task gate. |
| `max_task_attempts` | — | `3` | Number of inspector rejections a task may accumulate before escalating to the oracle. |
| `scrub_env_keys` | — | (empty) | Comma-separated env var names to strip from the worker subprocess. |
| `external_review_tool` | `--external-review-tool` | `codex` | Tool used as the inspector and oracle engine (`custom`, `codex`, `none`). |
| `custom_review_script` | `--custom-review-script` | — | Script path when `external_review_tool = custom`. |

The oracle requires interactive input for approval, so gated runs must have a terminal available.

## Credential isolation

The gate's guarantees hold even without token scrubbing, but to keep an inspector/approval credential
out of a potentially-compromised worker's reach, list its env var names in `scrub_env_keys`:

```ini
scrub_env_keys = UGABUGA_INSPECTOR_TOKEN, UGABUGA_ORACLE_TOKEN
```

Those variables are removed from the worker (Claude) subprocess environment while remaining available
to the parent and to the inspector/oracle subprocess. (`CLAUDECODE` is always stripped regardless, and
`ANTHROPIC_API_KEY` is stripped unless `--preserve-anthropic-api-key` is set — see the main README.)

A minimal wrapper that mints a per-run token the parent holds and the worker never sees:

```bash
#!/usr/bin/env bash
# ralphex-gated.sh — run ralphex with a fresh inspector token the worker can't read.
set -euo pipefail
export UGABUGA_INSPECTOR_TOKEN="$(openssl rand -hex 16)"
exec ralphex --inspector-gate \
  --external-review-tool=custom \
  --custom-review-script="$(dirname "$0")/scripts/opencode/opencode-review.sh" \
  "$@"
```

with `scrub_env_keys = UGABUGA_INSPECTOR_TOKEN` in config so the token is stripped from the worker.

## Verdict format

The inspector must respond with exactly one line:

```
VERDICT: done
VERDICT: reject | <SPECIFIC REASON THE WORKER MUST ADDRESS>
VERDICT: update | <why the task spec itself needs revision>
```

Parsing is case-insensitive and tolerant of surrounding text — the last matching `VERDICT:` line wins.
If the inspector returns nothing parseable, ralphex retries the inspection up to twice, then falls
back to a conservative `reject` (it never silently accepts unparseable output).

The oracle must respond with exactly two lines:

```
OLD: <exact substring currently in the plan>
NEW: <replacement text>
```

`OLD` must be a literal substring of the current plan, or the substitution errors out — the oracle
never silently changes nothing.

## Limitations

- The oracle's approval step needs an interactive terminal; it can't run fully unattended.
- The inspector, oracle, and gated-task prompts are built in code (not yet customizable prompt files).
- The interaction between worktree mode and the state DB path is not yet validated.
