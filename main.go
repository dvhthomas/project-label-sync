// project-label-sync bidirectionally syncs GitHub Projects v2 status
// fields with issue labels. Runs as a GitHub Action or standalone CLI.
//
// CLI usage:
//
//	go run . --token ghp_... --config .github/project-label-sync.yml
//	go run . --token ghp_... --config .github/project-label-sync.yml --apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	gh "github.com/dvhthomas/project-label-sync/github"
	applog "github.com/dvhthomas/project-label-sync/internal/log"
	"github.com/dvhthomas/project-label-sync/sync"
)

func main() {
	log.SetFlags(0)

	if err := run(); err != nil {
		applog.Error("%v", err)
		os.Exit(1)
	}
}

func run() error {
	// CLI flags (primary interface for local use).
	var (
		tokenFlag   string
		configFlag  string
		applyFlag   bool
		verboseFlag bool
	)
	flag.StringVar(&tokenFlag, "token", "", "GitHub PAT with project + repo scopes")
	flag.StringVar(&configFlag, "config", ".github/project-label-sync.yml", "Path to config file")
	flag.BoolVar(&applyFlag, "apply", false, "Apply changes (without this flag, only previews)")
	flag.BoolVar(&verboseFlag, "verbose", false, "Log every per-issue action (default: summary only)")
	flag.Parse()

	// Fall back to GitHub Actions inputs if flags aren't set.
	token := firstNonEmpty(tokenFlag, actionInput("TOKEN"), os.Getenv("GH_TOKEN"))
	configPath := firstNonEmpty(configFlag, actionInput("CONFIG"))
	apply := applyFlag || actionInput("APPLY") == "true"
	verbose := verboseFlag || actionInput("VERBOSE") == "true"

	if configPath == "" || configPath == ".github/project-label-sync.yml" {
		// Check if the default exists; if not and a flag wasn't explicitly set, error clearly.
		if _, err := os.Stat(configPath); os.IsNotExist(err) && configFlag == ".github/project-label-sync.yml" {
			configPath = ".github/project-label-sync.yml"
		}
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	if token == "" {
		return fmt.Errorf("token is required\n\nPass a classic PAT with 'project' and 'repo' scopes:\n  --token ghp_...\n  or set GH_TOKEN=ghp_...\n\nCreate one at: https://github.com/settings/tokens/new?scopes=project,repo&description=project-label-sync")
	}

	if apply {
		applog.Notice("APPLY mode — changes will be written to GitHub.")
	} else {
		applog.Notice("Preview mode — showing what would change. Use --apply to update issues.")
	}

	_, projectOwner, projectNumber, err := gh.ParseProjectURL(cfg.ProjectURL)
	if err != nil {
		return fmt.Errorf("parse project URL: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := gh.NewClient(token)
	project, err := client.ResolveProject(ctx, cfg.ProjectURL, cfg.Field)
	if err != nil {
		return err
	}

	applog.Notice("Project: %s (%d %s options: %s)",
		project.Title, len(project.Options), cfg.Field, formatOptions(project.Options))

	labels := gh.NewLabelManager(client.HTTPClient, token, !apply, verbose)
	syncer, err := sync.NewSyncer(project, client, labels, client, cfg.Mapping, cfg.Field, !apply, projectOwner, projectNumber)
	if err != nil {
		return err
	}
	syncer.ProjectURL = cfg.ProjectURL
	syncer.Verbose = verbose

	return syncer.Run(ctx)
}

// actionInput reads a GitHub Actions input from the environment.
func actionInput(name string) string {
	return strings.TrimSpace(os.Getenv("INPUT_" + strings.ToUpper(name)))
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func formatOptions(opts []gh.StatusOption) string {
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Name
	}
	return strings.Join(names, ", ")
}
