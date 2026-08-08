package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	cfg, err := loadConfig(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
		os.Exit(1)
	}

	engineNames := cfg.Engines
	if len(engineNames) == 0 {
		engineNames = []string{"pg"}
	}

	for _, pkg := range cfg.Packages {
		files, err := resolveFiles(pkg.Files)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
			os.Exit(1)
		}

		tags := tagList(cfg.Tags, pkg.Tags)

		// Group files by DSL package name
		pkgs := map[string]*pkgFiles{}
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}

			pf, err := parseFile(path, src)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}

			pkgName := pf.Package
			if _, ok := pkgs[pkgName]; !ok {
				pkgs[pkgName] = &pkgFiles{}
			}
			pkgs[pkgName].files = append(pkgs[pkgName].files, pf)
		}

		// Output base dir
		outBase := pkg.Path
		if outBase == "" {
			outBase = "."
		}

		// Generate each package
		for pkgName, pf := range pkgs {
			// Check for duplicate models
			models := map[string]bool{}
			for _, f := range pf.files {
				for _, m := range f.Models {
					if models[m.Name] {
						fmt.Fprintf(os.Stderr, "sqlgen: package %q: duplicate model %q\n", pkgName, m.Name)
						os.Exit(1)
					}
					models[m.Name] = true
				}
			}

			pkgDir := filepath.Join(outBase, pkgName)
			if err := os.MkdirAll(pkgDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}

			g := &generator{
				pkgPath:  pkgDir,
				pkgName:  pkgName,
				tags:     tags,
				engines:  engineNames,
				files:    pf.files,
			}
			if err := g.generate(); err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}
		}
	}

	fmt.Println("sqlgen: done")
}

type pkgFiles struct {
	files []*ParsedFile
}

var _ = strings.TrimSpace
