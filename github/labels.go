package github

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// LabelManager handles label operations via the gh CLI.
type LabelManager struct {
	Token  string
	DryRun bool
}

// NewLabelManager creates a LabelManager.
func NewLabelManager(token string, dryRun bool) *LabelManager {
	return &LabelManager{Token: token, DryRun: dryRun}
}

// EnsureLabelExists creates the label if it does not already exist.
// Uses a neutral gray color.
func (m *LabelManager) EnsureLabelExists(ctx context.Context, repo, labelName string) error {
	return withRetry(ctx, "ensure-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would ensure label %q exists on %s", labelName, repo)
			return nil
		}

		// gh label create is idempotent — it fails if the label already exists,
		// which we treat as success.
		cmd := exec.CommandContext(ctx, "gh", "label", "create", labelName,
			"--color", "ededed",
			"--description", "Synced from project board status",
			"--force",
			"-R", repo,
		)
		cmd.Env = append(cmd.Environ(), "GH_TOKEN="+m.Token)
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := strings.TrimSpace(string(out))
			// "already exists" is fine.
			if strings.Contains(outStr, "already exists") {
				return nil
			}
			if isTransient(outStr) {
				return &retryableError{err: fmt.Errorf("ensure label %q: %s: %w", labelName, outStr, err)}
			}
			return fmt.Errorf("ensure label %q on %s: %s: %w", labelName, repo, outStr, err)
		}
		log.Printf("::notice::Created label %q on %s", labelName, repo)
		return nil
	})
}

// AddLabel adds a label to an issue.
func (m *LabelManager) AddLabel(ctx context.Context, repo string, issueNumber int, labelName string) error {
	return withRetry(ctx, "add-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would add label %q to %s#%d", labelName, repo, issueNumber)
			return nil
		}

		cmd := exec.CommandContext(ctx, "gh", "issue", "edit",
			strconv.Itoa(issueNumber),
			"--add-label", labelName,
			"-R", repo,
		)
		cmd.Env = append(cmd.Environ(), "GH_TOKEN="+m.Token)
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := strings.TrimSpace(string(out))
			if isTransient(outStr) {
				return &retryableError{err: fmt.Errorf("add label: %s: %w", outStr, err)}
			}
			return fmt.Errorf("add label %q to %s#%d: %s: %w", labelName, repo, issueNumber, outStr, err)
		}
		log.Printf("::notice::Added label %q to %s#%d", labelName, repo, issueNumber)
		return nil
	})
}

// RemoveLabel removes a label from an issue.
func (m *LabelManager) RemoveLabel(ctx context.Context, repo string, issueNumber int, labelName string) error {
	return withRetry(ctx, "remove-label-"+labelName, 3, func() error {
		if m.DryRun {
			log.Printf("[dry-run] Would remove label %q from %s#%d", labelName, repo, issueNumber)
			return nil
		}

		cmd := exec.CommandContext(ctx, "gh", "issue", "edit",
			strconv.Itoa(issueNumber),
			"--remove-label", labelName,
			"-R", repo,
		)
		cmd.Env = append(cmd.Environ(), "GH_TOKEN="+m.Token)
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := strings.TrimSpace(string(out))
			// Label might already be removed; that is fine.
			if strings.Contains(outStr, "not found") || strings.Contains(outStr, "does not have") {
				return nil
			}
			if isTransient(outStr) {
				return &retryableError{err: fmt.Errorf("remove label: %s: %w", outStr, err)}
			}
			return fmt.Errorf("remove label %q from %s#%d: %s: %w", labelName, repo, issueNumber, outStr, err)
		}
		log.Printf("::notice::Removed label %q from %s#%d", labelName, repo, issueNumber)
		return nil
	})
}

// isTransient returns true for error messages that look like transient
// server-side failures.
func isTransient(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "500") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "try again")
}
