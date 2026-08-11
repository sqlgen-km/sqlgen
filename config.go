package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"github.com/sqlgen-km/sqlgen/meta"
)

// Config is the sqlg.yaml configuration.
type Config struct {
	Engines []string `yaml:"engines"` // defaults to ["pg"]
	Go      *GoCfg   `yaml:"go"`
	Java    *JavaCfg `yaml:"java"`
}

// GoCfg is the Go language configuration block.
type GoCfg struct {
	Tags     []string   `yaml:"tags"`
	Packages []GoPkgCfg `yaml:"packages"`
}

// GoPkgCfg is a single Go package configuration.
type GoPkgCfg struct {
	Out   string   `yaml:"out"`   // output directory
	Tags  []string `yaml:"tags"`  // per-package tag override
	Files []string `yaml:"files"` // glob patterns for .sql files
}

// JavaCfg is the Java language configuration block.
type JavaCfg struct {
	Packages []meta.JavaPkgCfg `yaml:"packages"`
}

// JavaPkgCfg is an alias for meta.JavaPkgCfg.
type JavaPkgCfg = meta.JavaPkgCfg

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
	if len(cfg.Engines) == 0 {
		cfg.Engines = []string{"pg"}
	}
	if cfg.Go == nil && cfg.Java == nil {
		return nil, fmt.Errorf("sqlg.yaml: at least one of 'go' or 'java' must be configured")
	}
	if cfg.Go != nil && len(cfg.Go.Packages) == 0 {
		return nil, fmt.Errorf("sqlg.yaml: go.packages must have at least one entry")
	}
	if cfg.Java != nil && len(cfg.Java.Packages) == 0 {
		return nil, fmt.Errorf("sqlg.yaml: java.packages must have at least one entry")
	}
	return &cfg, nil
}

// resolveFiles expands glob patterns.
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

var _ = strings.TrimSpace
