package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/golang"
	"github.com/sqlgen-km/sqlgen/languages/java"
)

func main() {
	cfg, err := loadConfig(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
		os.Exit(1)
	}

	engineNames := cfg.Engines

	// ── Go language ──
	if cfg.Go != nil {
		// Resolve Go engines once
		goEngineMap := make(map[string]engines.Engine, len(engineNames))
		for _, name := range engineNames {
			eng, err := getEngine(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: Go engine %q: %v\n", name, err)
				os.Exit(1)
			}
			goEngineMap[name] = eng
		}

		for _, pkg := range cfg.Go.Packages {
			files, err := resolveFiles(pkg.Files)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}

			tags := tagList(cfg.Go.Tags, pkg.Tags)

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

			outBase := pkg.Out
			if outBase == "" {
				outBase = "."
			}

			for pkgName, pf := range pkgs {
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

				g := &golang.Generator{
					PkgPath:     pkgDir,
					PkgName:     pkgName,
					Tags:        tags,
					EngineNames: engineNames,
					EngineMap:   goEngineMap,
					Files:       pf.files,
				}
				if err := g.Generate(); err != nil {
					fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
					os.Exit(1)
				}
			}
		}
		fmt.Println("sqlgen: go done")
	}

	// ── Java language ──
	if cfg.Java != nil {
		javaEngines := make([]java.Engine, 0, len(engineNames))
		for _, name := range engineNames {
			eng, err := getJavaEngine(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: Java engine %q: %v\n", name, err)
				os.Exit(1)
			}
			javaEngines = append(javaEngines, eng)
		}

		for _, pkg := range cfg.Java.Packages {
			files, err := resolveFiles(pkg.Files)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
				os.Exit(1)
			}

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
				if err := java.Generate(pf, pkg, javaEngines); err != nil {
					fmt.Fprintf(os.Stderr, "sqlgen: %v\n", err)
					os.Exit(1)
				}
			}
		}
		fmt.Println("sqlgen: java done")
	}
}

type pkgFiles struct {
	files []*ParsedFile
}

var _ = strings.TrimSpace
