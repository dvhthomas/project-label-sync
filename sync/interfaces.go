package sync

import "context"

// LabelSyncer abstracts label operations for testing.
type LabelSyncer interface {
	EnsureLabelExists(ctx context.Context, repo, labelName string) error
	AddLabel(ctx context.Context, repo string, issueNumber int, labelName string) error
	RemoveLabel(ctx context.Context, repo string, issueNumber int, labelName string) error
	CheckLabelsExist(ctx context.Context, repo string, labels []string) (existing, missing []string, err error)
}

// BoardUpdater abstracts project board mutations for testing.
type BoardUpdater interface {
	UpdateItemStatus(ctx context.Context, projectID, itemID, fieldID, optionID string) error
}
