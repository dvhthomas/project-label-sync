package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name: "valid config",
			content: `project-url: https://github.com/users/dvhthomas/projects/1
field: Status
mapping:
  "In Progress":
    - in-progress
  "In Review":
    - in-review
  Done:
    - done
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.ProjectURL != "https://github.com/users/dvhthomas/projects/1" {
					t.Errorf("got ProjectURL %q", cfg.ProjectURL)
				}
				if cfg.Field != "Status" {
					t.Errorf("got Field %q", cfg.Field)
				}
				if len(cfg.Mapping) != 3 {
					t.Errorf("got %d mapping entries, want 3", len(cfg.Mapping))
				}
				if labels := cfg.Mapping["In Progress"]; len(labels) != 1 || labels[0] != "in-progress" {
					t.Errorf("In Progress mapping = %v", labels)
				}
			},
		},
		{
			name: "field defaults to Status",
			content: `project-url: https://github.com/users/dvhthomas/projects/1
mapping:
  Done:
    - done
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Field != "Status" {
					t.Errorf("got Field %q, want Status", cfg.Field)
				}
			},
		},
		{
			name: "missing project-url",
			content: `mapping:
  Done:
    - done
`,
			wantErr: "config: project-url is required",
		},
		{
			name: "missing mapping",
			content: `project-url: https://github.com/users/dvhthomas/projects/1
`,
			wantErr: "config: mapping is required",
		},
		{
			name:    "invalid yaml",
			content: `{{{`,
			wantErr: "parse config",
		},
		{
			name: "quoted keys with spaces",
			content: `project-url: https://github.com/users/dvhthomas/projects/1
mapping:
  "In Progress":
    - in-progress
  "Code Review":
    - code-review
`,
			check: func(t *testing.T, cfg *Config) {
				if _, ok := cfg.Mapping["In Progress"]; !ok {
					t.Error("missing 'In Progress' key")
				}
				if _, ok := cfg.Mapping["Code Review"]; !ok {
					t.Error("missing 'Code Review' key")
				}
			},
		},
		{
			name: "multi-label mapping",
			content: `project-url: https://github.com/users/dvhthomas/projects/1
mapping:
  "In Progress":
    - in-progress
    - active
`,
			check: func(t *testing.T, cfg *Config) {
				labels := cfg.Mapping["In Progress"]
				if len(labels) != 2 {
					t.Fatalf("got %d labels, want 2", len(labels))
				}
				if labels[0] != "in-progress" || labels[1] != "active" {
					t.Errorf("got labels %v", labels)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadConfig(path)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
