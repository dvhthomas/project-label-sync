package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration file for project-label-sync.
type Config struct {
	ProjectURL string              `yaml:"project-url"`
	Field      string              `yaml:"field"`
	Mapping    map[string][]string `yaml:"mapping"`
}

// loadConfig reads and validates the configuration from a YAML file.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s\n\nCreate one with:\n\n  project-url: https://github.com/users/YOURNAME/projects/1\n  field: Status\n  mapping:\n    \"In Progress\":\n      - in-progress", path)
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.ProjectURL == "" {
		return nil, fmt.Errorf("config: project-url is required\n\nAdd to %s:\n\n  project-url: https://github.com/users/YOURNAME/projects/1", path)
	}
	if cfg.Field == "" {
		cfg.Field = "Status"
	}
	if len(cfg.Mapping) == 0 {
		return nil, fmt.Errorf("config: mapping is required\n\nAdd to %s:\n\n  mapping:\n    \"In Progress\":\n      - in-progress\n    Done:\n      - done", path)
	}

	return &cfg, nil
}
