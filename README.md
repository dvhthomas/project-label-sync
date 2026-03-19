# Project Label Sync

Bridge GitHub Projects and GitHub Labels — automatically.

## The problem

GitHub has two systems for tracking issue state, and they don't talk to each other.

**GitHub Projects** is where many teams manage work. You drag issues between columns like "In Progress" and "Done." It's visual, flexible, and built for planning.

**GitHub Labels** are where everything else reads state. Search queries, GitHub Actions triggers, CI workflows, external tools, metrics dashboards, and the GitHub API all filter by labels. Labels are portable, queryable, and universally understood.

If your team manages work on a project board, nothing outside that board knows about it. An issue sitting in "In Progress" on the board has no label to show for it. You can't search for in-progress issues, trigger a workflow when work starts, or feed a metrics tool that reads labels. The board is a silo.

Updating labels by hand every time you move a card is tedious, error-prone, and the first thing people stop doing.

## What this does

This tool watches your project board and your issue labels, and keeps them in sync. Move an issue to "In Progress" on the board and the `in-progress` label appears. Add a label directly and the board updates to match.

You define the mapping between board status values and labels in a config file. The tool handles the rest — creating missing labels, resolving conflicts by timestamp, and skipping statuses you don't care about.

It runs on a schedule via GitHub Actions, or locally from the command line. It never writes anything unless you explicitly pass `--apply`.

## Setup

### 1. Create a config file

Add `project-label-sync.yml` to your repo. The config tells the tool which project board to watch and how to map each status to one or more labels:

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

Each key under `mapping` must exactly match a value in your project's Status field (case-sensitive).

> [!TIP]
> You don't have to map every status. Maybe you don't want to tag anything in "Backlog" with an issue label — just leave it out of the config and it will be quietly ignored. If your board has "Backlog", "Triage", and "In Progress" but you only care about labeling active work, just map "In Progress" and leave the rest out. The preview output shows which statuses are unmapped so you can verify nothing was forgotten.

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

A status can map to multiple labels. When you drag an issue into that column, **all** the listed labels are applied. When the issue moves to a different status, all of them are removed and replaced with the new status's labels. Labels can contain spaces or special characters — just quote them in YAML. See the [examples](examples/) directory for more.

### 2. Create a personal access token

The GitHub Actions `GITHUB_TOKEN` cannot access project board data. You need a **classic** personal access token with `project` and `repo` scopes.

Create one here: https://github.com/settings/tokens/new?scopes=project,repo&description=project-label-sync

Add it as a repository secret named `PROJECT_PAT`.

> [!IMPORTANT]
> The token owner must have **write access to every repo** that has issues on the project board. Org-level project boards often span multiple repos — for example, an issue from `myorg/frontend` and another from `myorg/api` on the same board. The `repo` scope on the PAT grants access to all repos the user can access, but the user must actually be a collaborator or team member on each repo. If the token can't write to a particular repo, label sync will fail for issues in that repo (other repos still sync normally).

> [!NOTE]
> Fine-grained PATs do not support the Projects v2 GraphQL API. You must use a classic token.

### 3. Preview before you commit

Before enabling automation, run a preview to see what the tool would do. No issues are modified, no labels are created.

The easiest way is to add the workflow first (see step 5) **without** `apply: true`, then trigger it manually from the Actions tab. The Action defaults to preview mode — it shows what would change without touching anything.

Or run it locally:

```sh
# Install
go install github.com/dvhthomas/project-label-sync@latest

# Preview (GH_TOKEN also works)
project-label-sync --token ghp_... --config project-label-sync.yml
```

The output shows your configuration, which statuses are mapped and which are ignored, which labels exist or would be created, and a summary of what would change:

```
Preview mode — showing what would change. Use --apply to update issues.
Project: CalcMark Tracker (5 Status options: Backlog, Ready, In progress, In review, Done)
Configuration:
  Project: CalcMark Tracker (https://github.com/orgs/CalcMark/projects/1)
  Field: Status
  Mappings:
    "Done" → [done]
    "In progress" → [in-progress]
  Unmapped:
    "Backlog" — no labels (ignored)
    "Ready" — no labels (ignored)
    "In review" — no labels (ignored)
  Mode: Preview (no changes made — use --apply to update issues)

Label check on CalcMark/go-calcmark:
  ✗ done (will be created)
  ✗ in-progress (will be created)

Summary:
  Issues scanned: 1
  Already in sync: 0
  Would add labels: 0 issues
  Would remove labels: 0 issues
  Would update board: 0 issues
  Labels to create: 2
  Skipped (unmapped/closed): 1
  Errors: 0
```

The "Unmapped" section makes it easy to spot gaps — statuses with no label mapping. If you intended to leave them out, no action needed. If you forgot one, add it to your config.

Add `--verbose` to see the per-issue detail.

### 4. Add the GitHub Actions workflow

Add a workflow to your repo. Start **without** `apply` to preview:

```yaml
# .github/workflows/label-sync.yml
name: Sync Project Labels
on:
  schedule:
    - cron: '*/15 * * * *'   # every 15 minutes
  workflow_dispatch:
    inputs:
      apply:
        description: 'Apply changes (false = preview only)'
        type: boolean
        default: false
      verbose:
        description: 'Show per-issue detail'
        type: boolean
        default: false

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: dvhthomas/project-label-sync@v0.1.2
        with:
          token: ${{ secrets.PROJECT_PAT }}
          apply: ${{ github.event.inputs.apply || 'false' }}
          verbose: ${{ github.event.inputs.verbose || 'false' }}
```

Commit this, then go to the Actions tab and click **"Run workflow."** You'll see dropdowns for `apply` and `verbose` — leave both unchecked for a preview run. Check the log output to see what would change. The `actions/checkout` step is required so the config file is available.

### 5. Enable apply

When the preview looks right, either:

- **From the Actions UI:** Check the `apply` box and run again
- **For scheduled runs:** Change the default to `true` so the cron job applies changes automatically:

```yaml
      apply:
        description: 'Apply changes (false = preview only)'
        type: boolean
        default: true     # scheduled runs apply; manual runs still show the checkbox
```

Or from the CLI:

```sh
project-label-sync --token ghp_... --config project-label-sync.yml --apply
```

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
