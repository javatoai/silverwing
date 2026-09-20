# Tag builds

Tag operations are immediate single-call commands over an existing task. They
run the configured Git and Genbu integration themselves; callers should not
replace them with direct Git or Meegle commands.

`--task` always takes the task's directory name under the silverwing task root. A
record's own `folderName` is a display name and may differ, so always reuse the
command printed in `guidance` rather than rebuilding it from a record.

## Commands

```text
silverwing tag build --task <task-folder> [--service <service-id:module-id>]... --json
silverwing tag build --task <task-folder> --all-services --json
silverwing tag status --task <task-folder> --operation <operation-id> --json
silverwing tag history --task <task-folder> --json
silverwing tag retry --task <task-folder> --operation <operation-id> --json
silverwing tag workspace-check --task <task-folder> --operation <operation-id> --json
```

- `build` pushes the feature branch, merges it into the configured target
  branch when the service requires it, and creates and pushes the next test
  Tag. Repeat `--service` for several services of the task, or pass
  `--all-services` to build every Tag-enabled service as one batch; the two
  forms are mutually exclusive. Repeating the same module (or naming it once by
  selection key and once by repository id) builds it only once. It returns one
  report per service. A service that fails comes back as a failed report inside
  the successful envelope, so never read exit code 0 as "all services built":
  check every report's `state` and `retryKind`. A group or module with its Tag
  switch off is rejected up front as a command error, because retrying cannot
  fix a configuration choice.
- `history` lists the task's Tag records, newest first. It does not query Genbu,
  so its `retryKind` reflects each record's last stored Genbu result.
- `status` returns one record and, when the service has Genbu probing enabled,
  refreshes its Genbu build/UAT/production stages live first and persists the
  refreshed result. Poll it to learn whether a Tag finished building or has been
  released to UAT.
- `retry` re-runs the record in place on the path matching its state; the same
  record is updated, so history never gains duplicate rows. It refreshes Genbu
  before choosing the path, so a stale build failure in `history` never causes a
  needless re-Tag.
- `workspace-check` reports uncommitted changes in the record's feature
  worktree without touching it. It accepts only a conflicted or failed record,
  and returns `{"clean": bool, "changes": [...]}` instead of a report.

## Result shape

Every Tag record is wrapped in a report. `silverwing tag build` returns an array of
them, `status` and `retry` return one. Like every `silverwing` command, the payload
below arrives inside the `{"ok": true, "result": ...}` envelope on stdout; a
failure instead prints `{"ok": false, "error": "..."}` to stderr and exits 1.

```json
{
  "operation": {
    "operationId": "...",
    "state": "SUCCESS | CONFLICT | FAILED | PARTIAL | ...",
    "tag": "1.6.89.beta-10",
    "sourceBranch": "feature/...",
    "targetBranch": "release/test",
    "remote": "origin",
    "message": "失败原因或成功摘要",
    "conflictFiles": ["src/..."],
    "genbuStatus": {
      "build": "INITIAL | BUILDING | SUCCESS | FAILED | UNKNOWN",
      "uat": "INITIAL | BUILDING | SUCCESS | FAILED | UNKNOWN",
      "production": "INITIAL | BUILDING | SUCCESS | FAILED | UNKNOWN"
    }
  },
  "retryKind": "RESOLVE_CONFLICT | RETRY_INTERRUPTED | RETRY_BUILD | RESUME_PARTIAL | RETAG | NONE",
  "guidance": "中文修复指引，包含下一步要执行的确切命令"
}
```

`genbuStatus` also carries each stage's completion time, the last check time,
and a probe `failureReason` when the Genbu CLI itself could not be queried.
`guidance` is null exactly when `retryKind` is `NONE`.

## Self-repair loop

Dispatch on `retryKind`; `guidance` already contains the concrete command:

- `RESOLVE_CONFLICT`: the record names the source branch, the remote target
  branch, and the conflicted files. Merge the source into the target, resolve,
  commit and push the target, then run `silverwing tag retry`.
- `RETRY_INTERRUPTED`: a previous build stopped before merging or tagging;
  `silverwing tag retry` re-runs the whole flow safely.
- `RETRY_BUILD`: the local build failed; fix the cause in `message`, then
  `silverwing tag retry`.
- `RESUME_PARTIAL`: the local Tag was created but its push did not finish;
  `silverwing tag retry` only completes the push.
- `RETAG`: the Genbu pipeline build failed for an otherwise successful Tag;
  `silverwing tag retry` re-tags with the next version number on the same record.
- `NONE`: nothing to repair; use `status` to follow the Genbu stages.
