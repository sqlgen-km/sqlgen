package lsp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sqlgen-km/sqlgen/dsl"
	"github.com/sqlgen-km/sqlgen/languages/golang"
	"github.com/sqlgen-km/sqlgen/languages/java"
	"github.com/sqlgen-km/sqlgen/meta"
	"github.com/sqlgen-km/sqlgen/registry"
)

// ── helpers ──

func uriToPath(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		return u.Path
	}
	return uri
}

func splitLines(text string) []string {
	return strings.Split(text, "\n")
}

func lineAt(text string, line int) string {
	lines := splitLines(text)
	if line < 0 || line >= len(lines) {
		return ""
	}
	return lines[line]
}

func lineRange(line int) rng {
	return rng{
		Start: position{Line: line, Character: 0},
		End:   position{Line: line, Character: 0},
	}
}

// parseErrorLine extracts the 1-indexed line from a "path:line: message" error.
func parseErrorLine(msg, path string) int {
	if rest, ok := strings.CutPrefix(msg, path+":"); ok {
		if idx := strings.IndexByte(rest, ':'); idx >= 0 {
			if n, err := strconv.Atoi(rest[:idx]); err == nil {
				return n
			}
		}
	}
	if m := regexp.MustCompile(`:(\d+):`).FindStringSubmatch(msg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 1
}

func isWordChar(c byte) bool {
	return c == '@' || c == '.' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// wordAt returns the word under the cursor and its start column.
func wordAt(line string, col int) string {
	if col > len(line) {
		col = len(line)
	}
	start := col
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := col
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return line[start:end]
}

// ── diagnostics ──

func computeDiagnostics(uri, text string) []diagnostic {
	path := uriToPath(uri)
	pf, err := dsl.ParseFile(path, []byte(text))
	if err != nil {
		line := parseErrorLine(err.Error(), path)
		return []diagnostic{{
			Range:    lineRange(line - 1),
			Severity: SeverityError,
			Source:   "sqlgen",
			Message:  err.Error(),
		}}
	}

	var diags []diagnostic
	for _, q := range pf.Queries {
		if _, err := meta.BuildQuery(q); err != nil {
			diags = append(diags, diagnostic{
				Range:    lineRange(q.Line - 1),
				Severity: SeverityError,
				Source:   "sqlgen",
				Message:  err.Error(),
			})
		}
	}
	return diags
}

// ── completion ──

func scalarTypes() []string {
	return []string{
		"int64", "int32", "int", "float64", "string", "bool", "time.Time",
		"[]int64", "[]string", "*string", "*int64", "*bool",
	}
}

func directiveCompletions() []completionItem {
	return []completionItem{
		{Label: "package", Kind: completionKeyword, InsertText: "package: $1", Detail: "包名"},
		{Label: "model", Kind: completionKeyword, InsertText: "model: ${1:Name} { $2 }", Detail: "模型定义"},
		{Label: "param", Kind: completionKeyword, InsertText: "param: ${1:name} ${2:Type}", Detail: "方法参数"},
		{Label: "name", Kind: completionKeyword, InsertText: "name: ${1:Method} :${2:one}", Detail: "查询定义"},
		{Label: "@", Kind: completionKeyword, InsertText: "@${1:说明}", Detail: "文档注释"},
	}
}

var (
	modeRe        = regexp.MustCompile(`--\s*name:\s*\w+\s*:\s*(\w*)$`)
	modelRefRe    = regexp.MustCompile(`--\s*model:\s*(\w*)$`)
	modelScalarRe = regexp.MustCompile(`--\s*model\s+([A-Za-z\[\]*.]*)$`)
	paramTypeRe   = regexp.MustCompile(`--\s*param:\s*[\w,]+\s+([A-Za-z\[\]*.]*)$`)
	fieldTypeRe   = regexp.MustCompile(`--\s*model:\s*\w+\s*\{[^}]*[\s,]+([A-Za-z\[\]*.]*)$`)
)

func computeCompletions(uri, text string, pos position) []completionItem {
	line := lineAt(text, pos.Line)
	prefix := line
	if pos.Character < len(line) {
		prefix = line[:pos.Character]
	}
	trimmed := strings.TrimSpace(prefix)

	var items []completionItem
	add := func(more []completionItem) { items = append(items, more...) }

	// Directive keywords at line start.
	if trimmed == "" || trimmed == "--" {
		add(directiveCompletions())
	} else if strings.HasPrefix(trimmed, "-- ") {
		word := strings.TrimPrefix(trimmed, "-- ")
		if !strings.Contains(word, " ") && isDirectivePrefix(word) {
			add(directiveCompletions())
		}
	}

	// Mode completion after "-- name: Method :".
	if modeRe.MatchString(trimmed) {
		for _, m := range []string{"one", "many", "exec", "execrows"} {
			add([]completionItem{{Label: m, Kind: completionKeyword, Detail: "执行模式"}})
		}
	}

	// Model-name completion after "-- model:" (reference).
	if m := modelRefRe.FindStringSubmatch(trimmed); m != nil {
		if pf, err := dsl.ParseFile(uriToPath(uri), []byte(text)); err == nil {
			for _, mod := range pf.Models {
				add([]completionItem{{Label: mod.Name, Kind: completionStruct, Detail: "模型"}})
			}
		}
	}

	// Scalar-type completion in type positions.
	for _, re := range []*regexp.Regexp{modelScalarRe, paramTypeRe, fieldTypeRe} {
		if re.MatchString(trimmed) {
			for _, t := range scalarTypes() {
				add([]completionItem{{Label: t, Kind: completionKeyword, Detail: "类型"}})
			}
			break
		}
	}

	return items
}

func isDirectivePrefix(word string) bool {
	for _, d := range []string{"package", "model", "param", "name", "@"} {
		if strings.HasPrefix(d, word) {
			return true
		}
	}
	return false
}

// ── definition ──

var (
	modelRefLineRe = regexp.MustCompile(`--\s*model:\s*(\w+)\s*$`)
	modelDefRe     = regexp.MustCompile(`--\s*model:\s*(\w+)\s*[={]`)
	nameDirRe      = regexp.MustCompile(`--\s*name:\s*(\w+)\s*:\s*(\w+)`)
)

func computeDefinition(uri, text string, pos position) []location {
	line := lineAt(text, pos.Line)
	lines := splitLines(text)

	// Model reference → definition.
	if m := modelRefLineRe.FindStringSubmatch(line); m != nil {
		name := m[1]
		var locs []location
		for i, l := range lines {
			if mm := modelDefRe.FindStringSubmatch(l); mm != nil && mm[1] == name {
				locs = append(locs, location{URI: uri, Range: lineRange(i)})
			}
		}
		if len(locs) > 0 {
			return locs
		}
	}

	// @param reference → param declaration.
	word := wordAt(line, pos.Character)
	if strings.HasPrefix(word, "@") {
		base := strings.TrimPrefix(word, "@")
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		paramRe := regexp.MustCompile(`--\s*param:.*\b` + regexp.QuoteMeta(base) + `\b`)
		var locs []location
		for i, l := range lines {
			if paramRe.MatchString(l) {
				locs = append(locs, location{URI: uri, Range: lineRange(i)})
			}
		}
		return locs
	}

	return nil
}

// ── document symbols ──

func computeDocumentSymbols(uri, text string) []documentSymbol {
	lines := splitLines(text)
	var syms []documentSymbol
	for i, l := range lines {
		if m := modelDefRe.FindStringSubmatch(l); m != nil {
			syms = append(syms, documentSymbol{
				Name: m[1], Kind: symbolStruct,
				Range: lineRange(i), SelectionRange: lineRange(i),
			})
			continue
		}
		if m := nameDirRe.FindStringSubmatch(l); m != nil {
			syms = append(syms, documentSymbol{
				Name: m[1], Detail: ":" + m[2], Kind: symbolMethod,
				Range: lineRange(i), SelectionRange: lineRange(i),
			})
		}
	}
	return syms
}

// ── generate preview ──

// generatePreview runs the full generator on a single DSL file and returns
// a readable dump of the generated Go and Java sources.
func generatePreview(uri, text string) (string, error) {
	path := uriToPath(uri)
	pf, err := dsl.ParseFile(path, []byte(text))
	if err != nil {
		return "", err
	}

	var out strings.Builder

	// Go
	if err := previewGo(pf, &out); err != nil {
		return "", fmt.Errorf("go: %w", err)
	}

	// Java
	if err := previewJava(pf, &out); err != nil {
		return "", fmt.Errorf("java: %w", err)
	}

	return out.String(), nil
}

func previewGo(pf *meta.ParsedFile, out *strings.Builder) error {
	tmp, err := os.MkdirTemp("", "sqlgen-preview-go-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	g := &golang.Generator{
		PkgPath:     tmp,
		PkgName:     pf.Package,
		Tags:        []string{"json"},
		EngineNames: registry.EngineNames(),
		EngineMap:   registry.GoEngines(),
		Files:       []*meta.ParsedFile{pf},
	}
	if err := g.Generate(); err != nil {
		return err
	}

	out.WriteString("=== Go ===\n")
	return dumpDir(out, tmp, ".go")
}

func previewJava(pf *meta.ParsedFile, out *strings.Builder) error {
	tmp, err := os.MkdirTemp("", "sqlgen-preview-java-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	pkg := java.PkgCfg{
		ModelPackage:  "com.example.entity",
		MapperPackage: "com.example.mapper",
		Out:           tmp,
	}
	if err := java.Generate(pf, pkg, javaEngineSlice()); err != nil {
		return err
	}

	out.WriteString("\n=== Java ===\n")
	return dumpDir(out, tmp, ".java")
}

func javaEngineSlice() []java.Engine {
	m := registry.JavaEngines()
	engs := make([]java.Engine, 0, len(m))
	for _, name := range registry.EngineNames() {
		engs = append(engs, m[name])
	}
	return engs
}

func dumpDir(out *strings.Builder, dir, ext string) error {
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ext) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\n--- %s ---\n%s", rel, data)
	}
	return nil
}
