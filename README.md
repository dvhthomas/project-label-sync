# Project Label Sync

Bidirectional sync between GitHub Projects v2 status fields and issue labels.

Move an issue to "In Progress" on your board and the `in-progress` label appears. Add a label and the board updates to match. Runs on a schedule via GitHub Actions or locally via CLI.

## Quick Start

Create `.github/project-label-sync.yml` in your repo:

```yaml
project-url: https://github.com/users/dvhthomas/projects/1
field: Status
mapping:
  "In progress":
    - in-progress
  "In review":
    - in-review
  Done:
    - done
```

### Preview what would change

```sh
go run . --token ghp_... --config examples/dvhthomas-gh-velocity.yml
```

```
Preview mode — showing what would change. Use --apply to update issues.
Project: gh-velocity (3 Status options: In progress, In review, Done)
Configuration:
  Project: gh-velocity (https://github.com/users/dvhthomas/projects/1)
  Field: Status
  Mappings:
    "Done" → [done]
    "In progress" → [in-progress]
    "In review" → [in-review]
  Mode: Preview (no changes made — use --apply to update issues)
Label check on dvhthomas/gh-velocity:
  ✓ done (exists)
  ✓ in-progress (exists)
  ✓ in-review (exists)
Summary:
  Issues scanned: 0
  Already in sync: 0
  Would add labels: 0 issues
  Would remove labels: 0 issues
  Would update board: 0 issues
  Labels to create: 0
  Skipped (unmapped/closed): 0
  Errors: 0
```

### Preview against a larger project

```sh
go run . --token ghp_... --config examples/microsoft-ebpf-for-windows.yml
```

```
Preview mode — showing what would change. Use --apply to update issues.
Project: eBPF for Windows (3 Status options: Todo, In Progress, Done)
Configuration:
  Project: eBPF for Windows (https://github.com/orgs/microsoft/projects/2098)
  Field: Status
  Mappings:
    "Done" → [done]
    "In Progress" → [in-progress]
    "Todo" → [todo]
  Mode: Preview (no changes made — use --apply to update issues)
Label check on microsoft/ebpf-for-windows:
  ✗ done (will be created)
  ✗ in-progress (will be created)
  ✗ todo (will be created)
[add-label] microsoft/ebpf-for-windows#3668: board has "Todo" but no mapped label; adding "todo"
[add-label] microsoft/ebpf-for-windows#3667: board has "Todo" but no mapped label; adding "todo"
[add-label] microsoft/ebpf-for-windows#3666: board has "In Progress" but no mapped label; adding "in-progress"
[add-label] microsoft/ebpf-for-windows#3659: board has "In Progress" but no mapped label; adding "in-progress"
  ... (94 more issues)
Summary:
  Issues scanned: 98
  Already in sync: 0
  Would add labels: 98 issues
  Would remove labels: 0 issues
  Would update board: 0 issues
  Labels to create: 3
  Skipped (unmapped/closed): 0
  Errors: 0
```

### Apply changes

```sh
go run . --token ghp_... --config .github/project-label-sync.yml --apply
```

## GitHub Actions

```yaml
name: Sync Project Labels
on:
  schedule:
    - cron: '*/15 * * * *'
  workflow_dispatch:

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dvhthomas/project-label-sync@main
        with:
          token: ${{ secrets.PROJECT_PAT }}
          apply: true
```

The `actions/checkout` step is required so the config file is available in the workspace.

## Configuration

```yaml
# .github/project-label-sync.yml
project-url: https://github.com/users/yourname/projects/1   # required
field: Status                                                 # optional, defaults to "Status"
mapping:                                                      # required
  "In Progress":
    - in-progress
  Done:
    - done
  # Backlog intentionally omitted — no label sync for it
```

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `project-url` | Yes | | GitHub Projects v2 URL (user or org) |
| `field` | No | `Status` | Single-select field name to sync |
| `mapping` | Yes | | Map of field values to label name lists |

### Mapping

Each key is a project field value. Values are label names to sync. Omit a status to skip it.

A status can map to multiple labels:

```yaml
mapping:
  "In Progress":
    - in-progress
    - active
```

Labels are auto-created with a neutral gray color (`#ededed`) on first sync.

## How It Works

1. Searches for open issues in the project via GitHub Search API
2. Fetches each issue's board status and label history via GraphQL
3. Compares timestamps to determine which is newer (label or board)
4. In `--apply` mode: creates missing labels, adds/removes labels, updates board status

Conflicts are resolved by most-recent-write-wins. If a label was added more recently than the board was updated, the board changes to match. If the board was updated more recently, labels change to match.

## Token Requirements

Requires a classic PAT with `project` and `repo` scopes. `GITHUB_TOKEN` cannot access project data. Fine-grained PATs do not support the Projects v2 GraphQL API.

Store it as a repository secret (e.g., `PROJECT_PAT`).

## Pairing with gh-velocity

This Action complements [gh-velocity](https://github.com/dvhthomas/gh-velocity), which uses issue labels as lifecycle signals for cycle-time metrics. The mapping in your sync config should match your gh-velocity `lifecycle` config:

**project-label-sync** config:

```yaml
mapping:
  "In progress":
    - in-progress
  "In review":
    - in-review
  Done:
    - done
```

**gh-velocity** config:

```yaml
lifecycle:
  in-progress: active
  in-review: active
  done: closed
```

## Limitations

- Polling-based (cron schedule), not real-time
- GitHub Search API returns max 1000 results per query
- Requires classic PAT (fine-grained PATs don't support Projects v2)
- Single project per config file
- Open issues only (closed issues are skipped)
