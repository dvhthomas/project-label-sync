# Project Label Sync

Keep your GitHub issue labels in sync with your GitHub Projects board — automatically.

## The problem

You manage work on a GitHub Projects board. You drag issues to "In Progress" and "Done." But labels don't update to match, so any tool, workflow, or search that relies on labels is out of date. Updating labels by hand is tedious and easy to forget.

## What this does

This tool reads your project board and your issue labels, compares them, and reconciles the difference. If you move an issue to "In Progress" on the board, the `in-progress` label appears on the issue. If someone adds a label directly, the board updates to match.

It runs on a schedule (every 15 minutes, or whatever you choose) via GitHub Actions, or locally from the command line. It never writes anything unless you explicitly pass `--apply`.

## Setup

### 1. Create a config file

Add `.github/project-label-sync.yml` to your repo. The config tells the tool which project board to watch and how to map each status to one or more labels:

```yaml
project-url: https://github.com/orgs/myorg/projects/1
field: Status
mapping:
  "In Progress":
    - in-progress
  "In Review":
    - in-review
  Done:
    - done
```

Each key under `mapping` must exactly match a value in your project's Status field (case-sensitive). Statuses you leave out are ignored — if your board has "Backlog" and you don't list it, those issues won't get labels.

The `field` defaults to `Status` but can be any single-select field on your project. For example, the [GitHub Public Roadmap](https://github.com/orgs/github/projects/4247) uses a `Release Phase` field:

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

A status can map to multiple labels, and labels can contain spaces or special characters (just quote them in YAML). See the [examples](examples/) directory for more.

### 2. Create a personal access token

The GitHub Actions `GITHUB_TOKEN` cannot access project board data. You need a **classic** personal access token with `project` and `repo` scopes.

Create one here: https://github.com/settings/tokens/new?scopes=project,repo&description=project-label-sync

Add it as a repository secret named `PROJECT_PAT`.

> Fine-grained PATs do not support the Projects v2 GraphQL API. You must use a classic token.

### 3. Preview before you commit

Before enabling automation, run a preview to see what the tool would do. No issues are modified, no labels are created.

```sh
# Install
go install github.com/dvhthomas/project-label-sync@latest

# Preview
project-label-sync --token ghp_... --config .github/project-label-sync.yml
```

The output shows your configuration, which labels exist or would be created, and what would change on each issue:

```
Preview mode — showing what would change. Use --apply to update issues.
Project: eBPF for Windows Triage (3 Status options: Todo, In Progress, Done)
Configuration:
  Project: eBPF for Windows Triage (https://github.com/orgs/microsoft/projects/2098)
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

Add `--verbose` to see the per-issue detail.

### 4. Apply changes

When the preview looks right, add `--apply`:

```sh
project-label-sync --token ghp_... --config .github/project-label-sync.yml --apply
```

### 5. Automate with GitHub Actions

Add a workflow to run the sync on a schedule:

```yaml
# .github/workflows/label-sync.yml
name: Sync Project Labels
on:
  schedule:
    - cron: '*/15 * * * *'   # every 15 minutes
  workflow_dispatch:          # manual trigger for testing

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: dvhthomas/project-label-sync@v0.1.1
        with:
          token: ${{ secrets.PROJECT_PAT }}
          apply: true
```

The `actions/checkout` step is required so the config file is available.

## How conflicts are resolved

When the board says one thing and the labels say another, the tool compares timestamps:

- **Board was updated more recently** → labels change to match the board
- **Label was added more recently** → board changes to match the label

On first run, every issue gets labels from the board (there are no competing label timestamps yet).

## How it works under the hood

1. Searches for open issues in the project via the GitHub Search API (efficient — no full board scan)
2. Fetches each issue's board status and label history via GraphQL (batched, 20 issues per call)
3. Compares timestamps to determine which source is newer
4. In `--apply` mode: creates missing labels, adds/removes labels, updates board status
5. Only processes open issues — closed issues are skipped entirely

## Configuration reference

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `project-url` | Yes | | GitHub Projects v2 URL. Copy from your browser — `/views/1` suffixes are handled automatically. |
| `field` | No | `Status` | The single-select field to sync. |
| `mapping` | Yes | | Maps field values to label names. Each value must exactly match an option in the field (case-sensitive). |

## Common mistakes and how to fix them

### The tool says my status values don't match

```
ERROR: mapping contains "In Progress" but the project's Status field has no such option
ERROR: Available options: Backlog, Ready, In progress, In review, Done
```

Status names are **case-sensitive**. `"In Progress"` is not the same as `"In progress"`. Copy the exact names from the "Available options" line.

### The tool says my field doesn't exist

```
ERROR: project has no single-select field named "Priority"
```

Check the field name in your project's settings. Most projects use `Status`. Some use custom fields like `Release Phase`.

### The tool says a label is used by multiple statuses

```
ERROR: config error: the same label cannot be used for multiple statuses

  "in-progress" is used by statuses: "Ready", "In progress"
```

Each status must map to a distinct set of labels. If two statuses share a label, the tool can't tell which status an issue is in. Give each status its own label.

### I see warnings about unmapped statuses

```
WARNING: project status "Backlog" is not mapped (will be ignored)
```

This is informational. Issues in unmapped statuses won't get labels. This is usually intentional.

### What status names do other projects use?

Every project is different. Run the tool to see your project's options:

```
Project: my-project (4 Status options: Short term, Mid term, Long term, Released)
```

Real examples from public projects:

| Project | Field | Options |
|---------|-------|---------|
| GitHub Roadmap | Status | Q3 2025, Q4 2025, Q1 2026, Future |
| GitHub Roadmap | Release Phase | GA, Public Preview |
| Kubernetes 1.36 | Status | At risk for code freeze, Tracked for PRR freeze, ... (14 options) |
| grafana/k6 | Status | Short term, Mid term, Long term, Released |

## Limitations

- **Polling, not real-time.** The GitHub API does not support project board events as Actions triggers. The tool runs on a cron schedule.
- **1,000 issue limit.** The GitHub Search API returns at most 1,000 results. If your project has more than 1,000 open issues, some will not be synced.
- **Classic PAT required.** Fine-grained personal access tokens do not support the Projects v2 GraphQL API.
- **One project per config.** Each config file syncs one project. For multiple projects, use multiple config files and workflow steps.
- **Open issues only.** Closed issues are not synced.

## License

MIT
