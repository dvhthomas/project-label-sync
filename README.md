# Project Label Sync

Bidirectionally sync GitHub Projects v2 status fields with issue labels on a cron schedule.

## Quick start

```yaml
name: Sync Project Labels
on:
  schedule:
    - cron: '*/15 * * * *'  # every 15 minutes
  workflow_dispatch:

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: dvhthomas/project-label-sync@main
        with:
          project-url: 'https://github.com/users/yourname/projects/1'
          token: ${{ secrets.PROJECT_PAT }}
          field: Status
          mapping: |
            In Progress: in-progress
            In Review: in-review
            Done: done
          dry-run: 'false'
```

In this example, "Backlog" is intentionally omitted from the mapping -- issues with that board status are ignored entirely (no label added, no sync attempted).

A status value can map to multiple labels (comma-separated):
```yaml
mapping: |
  In Progress: in-progress, active
```

## Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `project-url` | Yes | | GitHub Projects v2 URL (user or org) |
| `token` | Yes | | Classic PAT with `project` + `repo` scopes |
| `field` | No | `Status` | Project field name to sync |
| `mapping` | Yes | | YAML mapping of field values to label names (one per line, `FieldValue: label-name`) |
| `dry-run` | No | `true` | Log changes without applying them |

## How it works

1. Fetches all open issues from the configured GitHub Projects v2 board via GraphQL
2. For each issue, compares the board's field value to issue labels using the configured mapping
3. Reconciles bidirectionally:
   - **Board changed**: If the board has a status but the issue has no matching mapped label, the mapped label(s) are added
   - **Label changed**: If a mapped label was added more recently than the board was updated, the board status is updated to match
   - **Conflict**: Most-recent-write-wins, comparing label event timestamps to board item timestamps
4. Competing mapped labels (multiple mapped labels from different statuses) are cleaned up automatically; the board status wins
5. Board status values not present in the mapping are silently ignored

### Label naming

Labels are explicitly mapped from board status values:

```yaml
mapping: |
  Todo: todo
  In Progress: in-progress
  Done: done
```

| Board Status | Label |
|-------------|-------|
| Todo | `todo` |
| In Progress | `in-progress` |
| Done | `done` |
| Backlog | *(not mapped -- ignored)* |

Labels are auto-created with a neutral gray color (`#ededed`) on first sync.

## Token requirements

The `GITHUB_TOKEN` provided by Actions **cannot** access GitHub Projects v2 data. You need a Classic Personal Access Token with:

- `repo` scope (for reading issues and managing labels)
- `project` scope (for reading and writing project board data)

Store it as a repository secret (e.g., `PROJECT_PAT`).

## Dry-run mode

Dry-run is **enabled by default**. The Action will log every decision it would make without performing any mutations. Set `dry-run: 'false'` to enable live mode.

## Limitations

- **Polling, not real-time**: Runs on a cron schedule, not in response to webhooks. Changes are picked up on the next run.
- **Single project**: Syncs one project board per workflow. Use multiple workflow jobs for multiple projects.
- **Open issues only**: Closed issues are skipped.
- **Single field**: Syncs one single-select field per workflow (default: "Status"). Other fields are ignored.
- **Single repo**: Each project item is synced to labels in its own repository.

## Recommended pairing

Use with [gh-velocity](https://github.com/dvhthomas/gh-velocity) to generate velocity and quality metrics from the same project data.
