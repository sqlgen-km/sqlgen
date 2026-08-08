package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGolden(t *testing.T) {
	writeMode := os.Getenv("WRITE_GOLDEN") == "1"
	
	tests := []struct {
		name      string
		subdir    string
		wantError bool
		engines   []string
	}{
		{name: "basic/users", subdir: "basic", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "inline_model/search", subdir: "inline_model", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "params/find", subdir: "params", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "complex/queries", subdir: "complex", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "write/mutations", subdir: "write", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "multi/users+admin", subdir: "multi", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "scalar/queries", subdir: "scalar", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "returning/roles", subdir: "returning", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "conflict/products", subdir: "conflict", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "exists/users", subdir: "exists", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "ilike/users", subdir: "ilike", engines: []string{"pg", "mssql", "oracle", "mysql"}},
		{name: "errors/dup_model", subdir: "errors/dup_model", wantError: true},
		{name: "errors/unmatched_field", subdir: "errors/unmatched_field", wantError: true},
		{name: "errors/returning_star", subdir: "errors/returning_star", wantError: true},
		{name: "errors/returning_multi", subdir: "errors/returning_multi", wantError: true},
		{name: "errors/returning_update", subdir: "errors/returning_update", wantError: true},
		{name: "errors/returning_delete", subdir: "errors/returning_delete", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlgenFiles, _ := filepath.Glob(filepath.Join("testdata", tt.subdir, "*.sql"))
			if len(sqlgenFiles) == 0 {
				t.Fatal("no .sql files found")
			}

			if tt.wantError {
				// Error test: expect a specific error from parse or generate
				testErrorCase(t, sqlgenFiles, tt.subdir)
				return
			}

			// Golden test: generate and compare against .golden files
			tmpDir := t.TempDir()

			var parsedFiles []*ParsedFile
			for _, f := range sqlgenFiles {
				src, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				pf, err := parseFile(f, src)
				if err != nil {
					t.Fatal(err)
				}
				parsedFiles = append(parsedFiles, pf)
			}

			// Validate consistent package name across files
			pkgName := parsedFiles[0].Package
			for _, pf := range parsedFiles {
				if pf.Package != pkgName {
					t.Fatalf("conflicting package names: %q vs %q", pkgName, pf.Package)
				}
			}

			// Check duplicate models across files
			models := map[string]bool{}
			for _, pf := range parsedFiles {
				for _, m := range pf.Models {
					if models[m.Name] {
						t.Fatalf("duplicate model %q across files", m.Name)
					}
					models[m.Name] = true
				}
			}

			g := &generator{
				pkgPath: tmpDir,
				pkgName: pkgName,
				tags:    []string{"json"},
				engines: tt.engines,
				files:   parsedFiles,
			}
			if err := g.generate(); err != nil {
				t.Fatal(err)
			}

			// Compare against golden files
			goldenDir := filepath.Join("testdata", tt.subdir)
			goldens, _ := filepath.Glob(filepath.Join(goldenDir, "*.golden"))
			if writeMode {
				// Write mode: generate golden files from current output
				ents, _ := os.ReadDir(tmpDir)
				for _, e := range ents {
					goldenName := e.Name() + ".golden"
					content, _ := os.ReadFile(filepath.Join(tmpDir, e.Name()))
					os.WriteFile(filepath.Join(goldenDir, goldenName), content, 0644)
					t.Logf("Wrote %s", goldenName)
				}
				// Remove old .sql.go.golden and .ast.go.golden files
				oldPatterns := []string{"*.sql.go.golden", "*.ast.go.golden"}
				for _, p := range oldPatterns {
					oldFiles, _ := filepath.Glob(filepath.Join(goldenDir, p))
					for _, of := range oldFiles {
						os.Remove(of)
						t.Logf("Removed old %s", filepath.Base(of))
					}
				}
				return
			}
			if len(goldens) == 0 {
				t.Fatal("no golden files found")
			}
			for _, gf := range goldens {
				baseFile := filepath.Base(gf)
				baseFile = baseFile[:len(baseFile)-len(".golden")]
				got := readFile(t, filepath.Join(tmpDir, baseFile))
				want := readFile(t, gf)
				if got != want {
					t.Errorf("%s mismatch:\n=== GOT ===\n%s\n=== WANT ===\n%s", baseFile, got, want)
				}
			}
		})
	}
}

// testErrorCase runs parse + generate on error test inputs.
func testErrorCase(t *testing.T, files []string, subdir string) {
	var parsedFiles []*ParsedFile
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		pf, err := parseFile(f, src)
		if err != nil {
			return // parse-level error is fine
		}
		parsedFiles = append(parsedFiles, pf)
	}

	if len(parsedFiles) == 0 {
		return
	}

	// Try generating — should fail
	tmpDir := t.TempDir()
	g := &generator{
		pkgPath: tmpDir,
		pkgName: parsedFiles[0].Package,
		tags:    []string{"json"},
		files:   parsedFiles,
	}
	if err := g.generate(); err == nil {
		t.Errorf("%s: expected generation error, got nil", files[0])
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
