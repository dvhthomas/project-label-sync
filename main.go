// project-label-sync is a GitHub Action that bidirectionally syncs
// GitHub Projects v2 status fields with issue labels.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	gh "github.com/dvhthomas/project-label-sync/github"
	"github.com/dvhthomas/project-label-sync/sync"
)

func main() {
	log.SetFlags(0) // GitHub Actions already provides timestamps.

	if err := run(); err != nil {
		log.Fatalf("::error::%v", err)
	}
}

func run() error {
	token := getInput("TOKEN")
	configPath := getInput("CONFIG")
	dryRun := getInput("DRY-RUN") != "false"

	if token == "" {
		return fmt.Errorf("input token is required")
	}
	if configPath == "" {
		configPath = ".github/project-label-sync.yml"
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	if dryRun {
		log.Println("::notice::Running in DRY-RUN mode. No mutations will be performed.")
	} else {
		log.Println("::notice::Running in LIVE mode. Mutations will be applied.")
	}

	// Parse project URL to extract owner and number for search queries.
	_, projectOwner, projectNumber, err := gh.ParseProjectURL(cfg.ProjectURL)
	if err != nil {
		return fmt.Errorf("parse project URL: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Resolve the project.
	client := gh.NewClient(token)
	project, err := client.ResolveProject(ctx, cfg.ProjectURL, cfg.Field)
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}

	log.Printf("::notice::Project: %s (%d %s options: %s)",
		project.Title, len(project.Options), cfg.Field, formatOptions(project.Options))

	// Run sync.
	labels := gh.NewLabelManager(client.HTTPClient, token, dryRun)
	syncer := sync.NewSyncer(project, client, labels, cfg.Mapping, cfg.Field, dryRun, projectOwner, projectNumber)

	return syncer.Run(ctx)
}

// getInput reads a GitHub Actions input from the environment.
// GitHub Actions sets INPUT_<NAME> with hyphens preserved in uppercase.
func getInput(name string) string {
	// GitHub Actions converts input names to uppercase and prefixes with INPUT_.
	envKey := "INPUT_" + strings.ToUpper(name)
	return strings.TrimSpace(os.Getenv(envKey))
}

func formatOptions(opts []gh.StatusOption) string {
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Name
	}
	return strings.Join(names, ", ")
}
