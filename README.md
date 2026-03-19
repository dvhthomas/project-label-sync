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
project-label-sync --token ghp_... --config examples/microsoft-ebpf-for-windows.yml
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
      - uses: dvhthomas/project-label-sync@v0.1.1
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

Labels can have spaces and special characters — just quote them:

```yaml
mapping:
  "In Progress":
    - "In Progress"
  "Code Review":
    - "Needs Review"
  Done:
    - "Done & Shipped"
```

### Real-world example: GitHub Public Roadmap

The [GitHub Public Roadmap](https://github.com/orgs/github/projects/4247) uses a **Release Phase** field (not Status) with values `GA` and `Public Preview`. They also use labels like `exploring`, `in design`, `preview`, `shipped`, and `ga` for lifecycle stages.

A sync config for this project would target the Release Phase field and map each phase to multiple existing labels:

```yaml
project-url: https://github.com/orgs/github/projects/4247
field: Release Phase
mapping:
  "Public Preview":
    - preview
    - "Public Preview"
  GA:
    - ga
    - shipped
```

This shows several features working together:
- Custom field name (`Release Phase` instead of the default `Status`)
- Multiple labels per status (`GA` syncs both `ga` and `shipped`)
- Labels with spaces (`"Public Preview"`)
- Unmapped lifecycle labels (`exploring`, `in design`) are left untouched

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

## Troubleshooting

### Config file not found

```
$ project-label-sync --config oops.yml
ERROR: config file not found: oops.yml

Create one with:

  project-url: https://github.com/users/YOURNAME/projects/1
  field: Status
  mapping:
    "In Progress":
      - in-progress
```

**Fix:** Create the config file. The default location is `.github/project-label-sync.yml`.

### Missing token

```
$ project-label-sync --config .github/project-label-sync.yml
ERROR: token is required

Pass a classic PAT with 'project' and 'repo' scopes:
  --token ghp_...
  or set GH_TOKEN=ghp_...

Create one at: https://github.com/settings/tokens/new?scopes=project,repo&description=project-label-sync
```

**Fix:** Create a classic PAT (not fine-grained) with `project` and `repo` scopes. Pass it via `--token` or `GH_TOKEN`.

### Wrong field name

```
$ project-label-sync --config my-config.yml
ERROR: project has no single-select field named "Priority"

Check the field name in your project settings (field names are case-sensitive).
The default is "Status". Your config has: field: Priority
```

**Fix:** The `field:` in your config doesn't match any single-select field on the project. Most projects use `Status` (the default). Check your project's field names in the project settings.

### Typo in status value

```
$ project-label-sync --config my-config.yml
Project: eBPF for Windows Triage (3 Status options: Todo, In Progress, Done)

WARNING: project status "Todo" is not mapped (will be ignored)
WARNING: project status "In Progress" is not mapped (will be ignored)
WARNING: project status "Done" is not mapped (will be ignored)
ERROR: mapping contains "Doen" but the project's Status field has no such option
ERROR: mapping contains "In Progres" but the project's Status field has no such option
ERROR: Available options: Done, In Progress, Todo
ERROR: config has 2 invalid mapping value(s)

The mapping keys must exactly match your project's Status field options (case-sensitive).
Copy the exact names from the 'Available options' list above into your config.
```

**Fix:** The mapping keys must match exactly (case-sensitive). Copy the names from the "Available options" line into your config.

### Case sensitivity

`"In Progress"` and `"In progress"` are different. The tool tells you the exact names:

```
Project: CalcMark Tracker (5 Status options: Backlog, Ready, In progress, In review, Done)
```

Use `"In progress"` (lowercase p), not `"In Progress"`.

### Duplicate labels across statuses

```
$ project-label-sync --config my-config.yml
ERROR: config error: the same label cannot be used for multiple statuses

  "in-progress" is used by statuses: "Ready", "In progress"

each status must map to unique labels so the sync can distinguish them
```

**Fix:** Each status must map to a unique set of labels. If "Ready" and "In Progress" both map to `in-progress`, the tool can't tell them apart and would corrupt your data. Give each status its own label:

```yaml
mapping:
  Ready:
    - ready
  "In progress":
    - in-progress
```

### Non-standard status names

Many projects don't use "Todo/In Progress/Done". Real examples:

| Project | Field | Options |
|---------|-------|---------|
| GitHub Roadmap | Status | Q3 2025, Q4 2025, Q1 2026, Future |
| GitHub Roadmap | Release Phase | GA, Public Preview |
| Kubernetes 1.36 | Status | At risk for code freeze, Tracked for PRR freeze, Deferred, ... (14 options) |
| grafana/k6 roadmap | Status | Short term, Mid term, Long term, Released |
| CalcMark | Status | Backlog, Ready, In progress, In review, Done |

**Tip:** Run the tool with any config to see your project's actual options. The startup output always shows them:

```
Project: my-project (4 Status options: Short term, Mid term, Long term, Released)
```

### Unmapped statuses (not an error)

```
WARNING: project status "Backlog" is not mapped (will be ignored)
```

This is informational — issues in "Backlog" won't get any labels. This is usually intentional (you only want labels for active work stages). To include it, add it to your mapping.

## Limitations

- Polling-based (cron schedule), not real-time
- GitHub Search API returns max 1000 results per query
- Requires classic PAT (fine-grained PATs don't support Projects v2)
- Single project per config file
- Open issues only (closed issues are skipped)
