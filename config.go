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
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.ProjectURL == "" {
		return nil, fmt.Errorf("config: project-url is required")
	}
	if cfg.Field == "" {
		cfg.Field = "Status"
	}
	if len(cfg.Mapping) == 0 {
		return nil, fmt.Errorf("config: mapping is required (map field values to labels)")
	}

	return &cfg, nil
}
