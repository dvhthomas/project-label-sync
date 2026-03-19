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
	// Read inputs from environment (GitHub Actions convention).
	projectURL := getInput("PROJECT-URL")
	token := getInput("TOKEN")
	fieldName := getInput("FIELD")
	mappingRaw := getInput("MAPPING")
	dryRun := getInput("DRY-RUN") != "false"

	if projectURL == "" {
		return fmt.Errorf("input project-url is required")
	}
	if token == "" {
		return fmt.Errorf("input token is required")
	}
	if fieldName == "" {
		fieldName = "Status"
	}
	if mappingRaw == "" {
		return fmt.Errorf("input mapping is required")
	}

	mapping, err := parseMapping(mappingRaw)
	if err != nil {
		return fmt.Errorf("parse mapping: %w", err)
	}

	if dryRun {
		log.Println("::notice::Running in DRY-RUN mode. No mutations will be performed.")
	} else {
		log.Println("::notice::Running in LIVE mode. Mutations will be applied.")
	}

	// Parse project URL to extract owner and number for search queries.
	_, projectOwner, projectNumber, err := gh.ParseProjectURL(projectURL)
	if err != nil {
		return fmt.Errorf("parse project URL: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Resolve the project.
	client := gh.NewClient(token)
	project, err := client.ResolveProject(ctx, projectURL, fieldName)
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}

	log.Printf("::notice::Project: %s (%d %s options: %s)",
		project.Title, len(project.Options), fieldName, formatOptions(project.Options))

	// Run sync.
	labels := gh.NewLabelManager(client.HTTPClient, token, dryRun)
	syncer := sync.NewSyncer(project, client, labels, mapping, fieldName, dryRun, projectOwner, projectNumber)

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

// parseMapping parses a multi-line "FieldValue: label-name" string into
// a map of field values to label name slices. A field value can map to
// multiple labels via comma separation.
func parseMapping(raw string) (map[string][]string, error) {
	result := make(map[string][]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid mapping line: %q (expected 'FieldValue: label-name')", line)
		}
		fieldValue := strings.TrimSpace(parts[0])
		labelsStr := strings.TrimSpace(parts[1])
		var labels []string
		for _, l := range strings.Split(labelsStr, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				labels = append(labels, l)
			}
		}
		if len(labels) == 0 {
			return nil, fmt.Errorf("mapping for %q has no labels", fieldValue)
		}
		result[fieldValue] = labels
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("mapping is empty")
	}
	return result, nil
}
