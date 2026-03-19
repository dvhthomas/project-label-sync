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
          dry-run: 'false'
```

## Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `project-url` | Yes | | GitHub Projects v2 URL (user or org) |
| `token` | Yes | | Classic PAT with `project` + `repo` scopes |
| `label-prefix` | No | `status:` | Prefix for status labels |
| `dry-run` | No | `true` | Log changes without applying them |

## How it works

1. Fetches all open issues from the configured GitHub Projects v2 board via GraphQL
2. For each issue, compares the board's Status field value to issue labels
3. Reconciles bidirectionally:
   - **Board changed**: If the board has a status but the issue has no matching label, the label is added
   - **Label changed**: If a status label was added more recently than the board was updated, the board status is updated to match
   - **Conflict**: Most-recent-write-wins, comparing label event timestamps to board item timestamps
4. Competing labels (multiple `status:*` labels) are cleaned up automatically; the board status wins

### Label naming

Board status values are mapped to labels using the configured prefix:

| Board Status | Label |
|-------------|-------|
| Todo | `status:Todo` |
| In Progress | `status:In Progress` |
| Done | `status:Done` |

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
- **Status field only**: Only syncs the "Status" single-select field. Other fields are ignored.
- **Single repo**: Each project item is synced to labels in its own repository.

## Recommended pairing

Use with [gh-velocity](https://github.com/dvhthomas/gh-velocity) to generate velocity and quality metrics from the same project data.
