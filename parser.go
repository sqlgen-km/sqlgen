package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlgen-km/sqlgen/meta"
)

// ── Type aliases for backward compatibility (definitions in meta/) ──

type ParsedFile = meta.ParsedFile
type ModelDef = meta.ModelDef
type FieldDef = meta.FieldDef
type QueryDef = meta.QueryDef
type ParamDef = meta.ParamDef
type FieldMap = meta.FieldMapDef

// inlineModel is a model declared inline in a query scope (may be unnamed).
type inlineModel struct {
	Name      string     // empty if unnamed; for scalar shorthand, this is the type name
	Fields    []FieldDef
	FieldMaps []FieldMap // field→column mapping, e.g. {id:user_id}
	Scalar    bool       // true for -- model int64 shorthand
}

// ── Parser ──

func parseFile(path string, src []byte) (*ParsedFile, error) {
	p := &fileParser{
		path:  path,
		src:   src,
		lines: strings.Split(string(src), "\n"),
	}
	return p.parse()
}

type fileParser struct {
	path          string
	src           []byte
	lines         []string
	pos           int
	pkg           string
	models        []ModelDef
	queries       []QueryDef
	pendingInline *inlineModel // unnamed model waiting for a query
}

func (p *fileParser) parse() (*ParsedFile, error) {
	if err := p.parsePackage(); err != nil {
		return nil, err
	}

	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])

		switch {
		case line == "":
			// Empty line: flush pending unnamed model (it has no query following)
			p.pendingInline = nil
			p.pos++

		case strings.HasPrefix(line, "-- model"):
			model, hasFields, err := p.parseModelLine()
			if err != nil {
				return nil, err
			}
			if hasFields {
				if model.Name != "" {
					p.models = append(p.models, ModelDef{Name: model.Name, Fields: model.Fields})
				} else {
					// Unnamed model with fields → could be auto-named by a query, or ignored
					p.pendingInline = model
				}
			} else if model.Name != "" {
				// Named model with no fields → reference-only, handled in query scope
				// Store for use as return type marker
				p.pendingInline = model
			}
			// Unnamed with no fields → nothing

		case strings.HasPrefix(line, "-- param:"):
			if err := p.parseQueryScope(); err != nil {
				return nil, err
			}

		case strings.HasPrefix(line, "-- name:"):
			// Queries without -- param: (e.g., SELECT COUNT(*))
			if err := p.parseQueryScope(); err != nil {
				return nil, err
			}

		default:
			p.pos++
		}
	}

	// Flush any remaining unnamed model (no query follows)
	p.pendingInline = nil

	// Validate: no duplicate model names
	seen := map[string]bool{}
	for _, m := range p.models {
		if seen[m.Name] {
			return nil, fmt.Errorf("%s: duplicate model %q", p.path, m.Name)
		}
		seen[m.Name] = true
	}

	return &ParsedFile{
		Package: p.pkg,
		Models:  p.models,
		Queries: p.queries,
	}, nil
}

// -------------------- package --------------------

func (p *fileParser) parsePackage() error {
	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])
		p.pos++
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "--") {
			content := strings.TrimSpace(strings.TrimPrefix(line, "--"))
			if strings.HasPrefix(content, "package:") {
				p.pkg = strings.TrimSpace(strings.TrimPrefix(content, "package:"))
				if p.pkg == "" {
					return fmt.Errorf("%s: empty package name", p.path)
				}
				return nil
			}
			return fmt.Errorf("%s:%d: first directive must be -- package:", p.path, p.pos)
		}
		return fmt.Errorf("%s:%d: first directive must be -- package:", p.path, p.pos)
	}
	return fmt.Errorf("%s: missing -- package: directive", p.path)
}

// -------------------- model line --------------------

// parseModelLine parses a single -- model: line.
// Returns model with fields, hasFields flag, and error.
// Lines handled:
//
//	-- model: Name { field Type, ... }      → named + fields
//	-- model: Name                           → named, no fields (ref or scalar)
//	-- model { field Type, ... }             → unnamed + fields
//	-- model int                             → scalar shorthand
//	-- model                                 → unnamed, no fields (skip)
func (p *fileParser) parseModelLine() (*inlineModel, bool, error) {
	line := strings.TrimSpace(p.lines[p.pos])
	p.pos++
	content := strings.TrimPrefix(line, "--")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "model")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, ":")
	content = strings.TrimSpace(content)

	if content == "" {
		return &inlineModel{}, false, nil
	}

	braceIdx := strings.IndexByte(content, '{')
	if braceIdx < 0 {
		// No braces → name or scalar type
		name := strings.TrimSpace(content)
		if name == "" {
			return &inlineModel{}, false, nil
		}
		// Check for equals sign: "Name={...}" form
		if eqIdx := strings.IndexByte(name, '='); eqIdx >= 0 {
			modelName := strings.TrimSpace(name[:eqIdx])
			mapStr := strings.TrimSpace(name[eqIdx+1:])
			// Expect {field:col, ...}
			if strings.HasPrefix(mapStr, "{") && strings.HasSuffix(mapStr, "}") {
				maps, err := parseFieldMaps(mapStr[1 : len(mapStr)-1])
				if err != nil {
					return nil, false, fmt.Errorf("%s:%d: %w", p.path, p.pos, err)
				}
				return &inlineModel{Name: modelName, FieldMaps: maps}, false, nil
			}
			return nil, false, fmt.Errorf("%s:%d: expected {field:column,...} after =", p.path, p.pos)
		}
		if isScalarType(name) {
			return &inlineModel{Name: name, Scalar: true}, false, nil
		}
		return &inlineModel{Name: name}, false, nil
	}

	// Has braces → fields declaration or field mapping
	name := strings.TrimSpace(content[:braceIdx])

	// Check for ={...} field mapping: "User={id:user_id}"
	if strings.HasSuffix(name, "=") {
		modelName := strings.TrimSuffix(name, "=")
		maps, err := parseFieldMaps(content[braceIdx+1 : len(content)-1])
		if err != nil {
			return nil, false, fmt.Errorf("%s:%d: %w", p.path, p.pos, err)
		}
		return &inlineModel{Name: modelName, FieldMaps: maps}, false, nil
	}

	// Pure field declaration: "Name { field Type }"
	fieldsStr := content[braceIdx+1:]
	closeIdx := strings.IndexByte(fieldsStr, '}')
	if closeIdx < 0 {
		return nil, false, fmt.Errorf("%s:%d: missing closing '}' in model", p.path, p.pos)
	}
	fieldsStr = fieldsStr[:closeIdx]

	fields, err := parseFields(fieldsStr)
	if err != nil {
		return nil, false, fmt.Errorf("%s:%d: %w", p.path, p.pos, err)
	}

	return &inlineModel{Name: name, Fields: fields}, true, nil
}

// isScalarType reports whether name is a known scalar type (including pointer).
func isScalarType(name string) bool {
	switch name {
	case "string", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "bool", "byte",
		"*string", "*int", "*int8", "*int16", "*int32", "*int64",
		"*uint", "*uint8", "*uint16", "*uint32", "*uint64",
		"*float32", "*float64", "*bool", "*byte",
		"time.Time", "*time.Time":
		return true
	}
	return false
}

// parseFieldMaps parses "col:Field, col" into FieldMap pairs.
// Left of : is SQL column, right is Go field name (PascalCase).
func parseFieldMaps(s string) ([]FieldMap, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := splitComma(s)
	var maps []FieldMap
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) == 1 {
			// "id" → default PascalCase
			col := strings.TrimSpace(kv[0])
			maps = append(maps, FieldMap{Field: meta.ToPascal(col), Column: col})
		} else {
			// "owner_name:Count" → 左=SQL列, 右=Go字段(PascalCase expected)
			col := strings.TrimSpace(kv[0])
			field := strings.TrimSpace(kv[1])
			// Accept both "count" and "Count" — always PascalCase
			if field == strings.ToLower(field) {
				field = meta.ToPascal(field)
			}
			maps = append(maps, FieldMap{Field: field, Column: col})
		}
	}
	return maps, nil
}

func parseFields(s string) ([]FieldDef, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := splitComma(s)
	fields := make([]FieldDef, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens := strings.Fields(part)
		if len(tokens) != 2 {
			return nil, fmt.Errorf("invalid field %q: expected 'name Type'", part)
		}
		fields = append(fields, FieldDef{
			Name: meta.ToPascal(tokens[0]),
			Type: tokens[1],
		})
	}
	return fields, nil
}

// -------------------- query scope --------------------

// parseQueryScope parses a query: -- param: + -- name: + [-- model:] + SQL.
// Uses pendingInline as return type if present.
func (p *fileParser) parseQueryScope() error {
	q := QueryDef{
		Src:  p.path,
		Line: p.pos + 1,
	}

	foundName := false
	var returnModel *inlineModel

	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])

		switch {
		case strings.HasPrefix(line, "-- @"):
			content := strings.TrimSpace(strings.TrimPrefix(line, "--"))
			content = strings.TrimSpace(strings.TrimPrefix(content, "@"))
			q.Doc = append(q.Doc, content)
			p.pos++

		case strings.HasPrefix(line, "-- param:"):
			if foundName {
				return fmt.Errorf("%s:%d: -- param: must come before -- name:", p.path, p.pos+1)
			}
			if err := p.parseParam(&q); err != nil {
				return err
			}

		case strings.HasPrefix(line, "-- name:"):
			if err := p.parseName(&q); err != nil {
				return err
			}
			foundName = true

		case strings.HasPrefix(line, "-- model"):
			model, hasFields, err := p.parseModelLine()
			if err != nil {
				return err
			}

			if hasFields && model.Name == "" {
				// Unnamed with fields → auto-name from method name
				// Name will be set when we know the method name
				model.Name = "" // will be set later
			}
			if model.Name != "" || hasFields {
				returnModel = model
			}
			// -- model with no name and no fields → ignore

		case line == "":
			// Empty line ends query scope — but we haven't found SQL yet
			// This means the directives are incomplete
			if foundName {
				return fmt.Errorf("%s:%d: query %q: missing SQL body", p.path, q.Line, q.Name)
			}
			p.pos++
			// Put back the pending inline model for the next scope
			if returnModel != nil {
				p.pendingInline = returnModel
			}
			return nil

		default:
			// Not a directive → SQL body
			if !foundName {
				return fmt.Errorf("%s:%d: unexpected line %q, expected -- name:", p.path, p.pos+1, line)
			}
			if err := p.parseSQL(&q); err != nil {
				return err
			}

			// Resolve return type
			if err := p.resolveReturn(&q, returnModel); err != nil {
				return err
			}

			p.queries = append(p.queries, q)
			p.pendingInline = nil // consumed
			return nil
		}
	}

	// EOF without SQL — if we have query directives, error
	if foundName {
		return fmt.Errorf("%s:%d: query %q: missing SQL body", p.path, q.Line, q.Name)
	}
	return nil
}

// resolveReturn determines the return type from the last -- model in scope.
func (p *fileParser) resolveReturn(q *QueryDef, rm *inlineModel) error {
	if q.Mode == "exec" || q.Mode == "execrows" {
		q.Return = ""
		return nil
	}

	if rm == nil {
		rm = p.pendingInline
	}

	if rm == nil {
		return fmt.Errorf("%s:%d: query %q (:one/:many) requires a -- model in scope", p.path, q.Line, q.Name)
	}

	if rm.Scalar {
		// -- model int64 shorthand: return scalar type directly
		q.Return = rm.Name
		q.IsScalar = true
		return nil
	}

	if len(rm.FieldMaps) > 0 {
		q.Return = rm.Name
		q.FieldMaps = rm.FieldMaps
		return nil
	}

	if rm.Name == "" && len(rm.Fields) > 0 {
		rm.Name = q.Name
		p.models = append(p.models, ModelDef{Name: rm.Name, Fields: rm.Fields})
	} else if rm.Name != "" && len(rm.Fields) > 0 {
		found := false
		for _, m := range p.models {
			if m.Name == rm.Name {
				found = true
				break
			}
		}
		if !found {
			p.models = append(p.models, ModelDef{Name: rm.Name, Fields: rm.Fields})
		}
	}

	q.Return = rm.Name
	q.IsScalar = false
	return nil
}

// -------------------- directive parsers --------------------

func (p *fileParser) parseParam(q *QueryDef) error {
	line := strings.TrimSpace(p.lines[p.pos])
	p.pos++
	content := strings.TrimPrefix(line, "--")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "param:")
	content = strings.TrimSpace(content)

	if content == "" {
		return nil
	}

	parts := splitComma(content)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens := strings.Fields(part)
		if len(tokens) != 2 {
			return fmt.Errorf("%s:%d: invalid param %q", p.path, p.pos, part)
		}
		q.Params = append(q.Params, ParamDef{
			Name: tokens[0],
			Type: tokens[1],
		})
	}
	return nil
}

func (p *fileParser) parseName(q *QueryDef) error {
	line := strings.TrimSpace(p.lines[p.pos])
	p.pos++
	content := strings.TrimPrefix(line, "--")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "name:")
	content = strings.TrimSpace(content)

	tokens := strings.Fields(content)
	if len(tokens) != 2 {
		return fmt.Errorf("%s:%d: invalid name directive, expected 'MethodName :mode'", p.path, p.pos)
	}

	q.Name = tokens[0]
	mode := strings.TrimPrefix(tokens[1], ":")
	switch mode {
	case "one", "many", "exec", "execrows":
		q.Mode = mode
	default:
		return fmt.Errorf("%s:%d: unknown mode %q", p.path, p.pos, tokens[1])
	}
	return nil
}

// parseSQL reads SQL lines until next directive or empty line that ends scope.
func (p *fileParser) parseSQL(q *QueryDef) error {
	var lines []string
	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])
		if line == "" || strings.HasPrefix(line, "--") {
			// Empty line or comment/directive ends SQL body
			break
		}
		lines = append(lines, line)
		p.pos++
	}

	if len(lines) == 0 {
		return fmt.Errorf("%s:%d: missing SQL body for query %q", p.path, q.Line, q.Name)
	}

	q.SQL = strings.Join(lines, "\n")
	return nil
}

// -------------------- Helpers --------------------

func splitComma(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<', '{', '(':
			depth++
		case '>', '}', ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func toCamel(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if i == 0 {
			parts[i] = strings.ToLower(p)
		} else if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func toPascalFromPath(name string) string {
	return meta.ToPascal(strings.ReplaceAll(name, "-", "_"))
}

var _ = strconv.Itoa
