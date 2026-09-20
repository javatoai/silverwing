---
name: silverwing
description: Build, inspect, follow, and safely retry silverwing test Tags with the installed silverwing CLI for an existing silverwing task. Use when the user explicitly invokes $silverwing for a test Tag operation; do not use for unrelated Git work.
---

# silverwing Test Tag CLI

Use this Skill only when the user explicitly invokes `$silverwing` for a test Tag on
an existing silverwing task. Use the installed `silverwing` command rather than substituting
arbitrary Git commands.

## Before a mutating command

`history`, `status`, and `workspace-check` are read-only. `build` and `retry`
can push branches, merge a configured target branch, or create and push a Tag.
Before the first `build` or any `retry`, confirm the exact task directory and
selected service/module with the user.

`--task` is the task directory name beneath silverwing's configured task root, not a
display title. To build selected modules, repeat
`--service <service-id:module-id>`; use `--all-services` only when the user
has confirmed every Tag-enabled module in the task.

## Workflow

1. When the operation ID is unknown, run
   `silverwing tag history --task <task-folder> --json`.
2. Build with either selected `--service` values or `--all-services`:
   `silverwing tag build --task <task-folder> ... --json`.
3. Follow a completed build with
   `silverwing tag status --task <task-folder> --operation <operation-id> --json`.
   It refreshes configured Genbu build, UAT, and production stages.
4. For a conflicted or failed record, use
   `silverwing tag workspace-check --task <task-folder> --operation <operation-id> --json`
   before deciding whether the worktree is ready to retry.
5. Read `retryKind` and `guidance` before retrying. Do not force-push, rewrite
   history, or use direct Git commands to bypass a reported failure.

## Reading results

Every command writes JSON. A command-level failure arrives on stderr as
`{"ok": false, "error": "..."}` and exits non-zero; report that error and
stop. A successful envelope is `{"ok": true, "result": ...}`.

For `build`, inspect every result entry rather than trusting the process exit
code. Each report contains:

- `operation.state` — for example `SUCCESS`, `CONFLICT`, `FAILED`, or
  `PARTIAL`.
- `operation.message` — the Git, build, or remote error detail.
- `operation.conflictFiles` — files involved in an automatic merge conflict.
- `retryKind` and `guidance` — the supported next step for a non-successful
  record.

Report the state, Tag (when present), failure message, conflict files, and
guidance faithfully. A `CONFLICT` record needs manual resolution before a
confirmed `silverwing tag retry`; a `PARTIAL` record uses retry to finish the recorded
Tag; a failed Genbu build uses retry to produce the next Tag version.

Read [the Tag build reference](references/tag-builds.md) for the complete
command forms, result shape, and retry semantics.
