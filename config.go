package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the sqlg.yaml configuration.
type Config struct {
	Tags     []string `yaml:"tags"`
	Engines  []string `yaml:"engines"` // defaults to ["pg"]
	Packages []PkgCfg `yaml:"packages"`
}

// PkgCfg is a single package configuration.
type PkgCfg struct {
	Path  string   `yaml:"path"`  // output directory (default ".")
	Tags  []string `yaml:"tags"`  // per-package tag override
	Files []string `yaml:"files"` // glob patterns for .sql files
}

// loadConfig reads sqlg.yaml from dir.
func loadConfig(dir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "sqlg.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read sqlg.yaml: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse sqlg.yaml: %w", err)
	}
	if len(cfg.Packages) == 0 {
		return nil, fmt.Errorf("sqlg.yaml: at least one package required")
	}
	return &cfg, nil
}

// resolveFiles expands glob patterns in a package config.
func resolveFiles(files []string) ([]string, error) {
	var out []string
	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("glob %q: no matching files", pattern)
		}
		out = append(out, matches...)
	}
	return out, nil
}

// tagList returns the resolved tag list for a package.
func tagList(global, local []string) []string {
	if len(local) > 0 {
		return local
	}
	return global
}

// splitTags parses "json,yaml" → ["json","yaml"]
func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
