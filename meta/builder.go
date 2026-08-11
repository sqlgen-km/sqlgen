package meta

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

// QueryBuilt holds pre-built query data for a single query.
type QueryBuilt struct {
	Q           QueryDef
	Stmt        ast.Statement
	Prep        *SQLPrep
	Src         string // Go source for AST literal
	ColumnCount int    // number of SQL result columns (0 if unknown)
	Columns     []string // SQL result column names (from SELECT or RETURNING)
}

// paramIdx tracks replacement of "_" params during AST walk.
var paramIdx int

// BuildRunnerSpecs builds RunnerSpecs and Models for a ParsedFile.
func BuildRunnerSpecs(files []ParsedFile, f *ParsedFile) ([]engines.RunnerSpec, []engines.Model, error) {
	models := CollectModels(files)

	var specs []engines.RunnerSpec
	for _, q := range f.Queries {
		qb, err := BuildQuery(q)
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", q.Src, q.Line, err)
		}
		kind := DetermineRunnerKind(qb)
		params := ResolveRunnerParams(qb, models)
		specs = append(specs, engines.RunnerSpec{
			Name:      LowerFirst(q.Name),
			Kind:      kind,
			Query:     q.Name,
			IsScalar:  q.IsScalar,
			HasILIKE:  qb.Prep.HasILIKE,
			ModelType: q.Return,
			Params:    params,
			Stmt:      qb.Stmt,
		})
	}

	var engineModels []engines.Model
	for _, name := range SortedKeys(models) {
		m := models[name]
		fields := make([]engines.Field, len(m.Fields))
		for i, fd := range m.Fields {
			fields[i] = engines.Field{Name: fd.Name, Type: fd.Type}
		}
		engineModels = append(engineModels, engines.Model{
			Name:   m.Name,
			Fields: fields,
		})
	}

	return specs, engineModels, nil
}

// CollectModels gathers all models, deduplicates.
func CollectModels(files []ParsedFile) map[string]ModelDef {
	out := map[string]ModelDef{}
	for _, f := range files {
		for _, m := range f.Models {
			out[m.Name] = m
		}
	}
	return out
}

// BuildQuery preprocesses and parses a query.
func BuildQuery(q QueryDef) (QueryBuilt, error) {
	prep := PreprocessSQL(q.SQL)

	stmt, err := ParseSQLStmt(prep.CleanedSQL)
	if err != nil {
		return QueryBuilt{}, fmt.Errorf("query %q (cleaned: %q): %w", q.Name, prep.CleanedSQL, err)
	}

	// Post-process: replace placeholder "_" params with actual names from prep.Params
	replaceParams(stmt, prep.Params)

	// Attach ON CONFLICT if extracted
	if prep.OnConflict != nil {
		if ins, ok := stmt.(*ast.InsertStmt); ok {
			oc := &ast.OnConflict{
				Columns:  prep.OnConflict.Columns,
				DoUpdate: prep.OnConflict.DoUpdate,
			}
			for _, s := range prep.OnConflict.Sets {
				oc.Sets = append(oc.Sets, ast.SetClause{Col: s.Col, Val: ast.Expr{Kind: ast.ExprCol, Col: s.Val}})
			}
			ins.OnConflict = oc
		}
	}

	// Detect silently dropped clauses (vitess parsing gaps)
	if strings.Contains(strings.ToUpper(q.SQL), "GROUP BY") {
		s, ok := stmt.(*ast.SelectStmt)
		if !ok || len(s.GroupBy) == 0 {
			return QueryBuilt{}, fmt.Errorf("query %q: GROUP BY clause was silently dropped by parser (SQL: %q)", q.Name, q.SQL)
		}
	}

	// RETURNING validation: only INSERT with single-column RETURNING is supported
	if len(prep.Returning) > 0 || prep.HasStar {
		switch s := stmt.(type) {
		case *ast.InsertStmt:
			if prep.HasStar {
				return QueryBuilt{}, fmt.Errorf("query %q: RETURNING * not supported; use RETURNING col for single column", q.Name)
			}
			if len(prep.Returning) > 1 {
				return QueryBuilt{}, fmt.Errorf("query %q: multi-column RETURNING not supported; use a single column", q.Name)
			}
			s.Returning = prep.Returning
		case *ast.UpdateStmt:
			return QueryBuilt{}, fmt.Errorf("query %q: UPDATE RETURNING not supported", q.Name)
		case *ast.DeleteStmt:
			return QueryBuilt{}, fmt.Errorf("query %q: DELETE RETURNING not supported", q.Name)
		}
	}

	src := formatStmt(stmt)
	cols := resultColumns(stmt, prep)
	// Use raw SQL to resolve aliases that vitess discards
	if len(cols) > 0 {
		cols = resolveAliases(q.SQL, cols)
	}

	return QueryBuilt{Q: q, Stmt: stmt, Prep: prep, Src: src, ColumnCount: len(cols), Columns: cols}, nil
}

// replaceParams walks an AST and replaces Param("_") placeholders
// with the correct names from refs, in order.
func replaceParams(stmt ast.Statement, refs []ParamRef) {
	paramIdx = 0
	walkStmt(stmt, func(e *ast.Expr) {
		if e.Kind == ast.ExprParam && e.Param == "_" {
			if paramIdx < len(refs) {
				e.Param = refs[paramIdx].Field
				paramIdx++
			}
		}
	})
}

// walkStmt walks all expression nodes in a statement.
func walkStmt(stmt ast.Statement, fn func(*ast.Expr)) {
	switch s := stmt.(type) {
	case *ast.SelectStmt:
		for i := range s.Columns {
			walkExpr(&s.Columns[i], fn)
		}
		if s.Where != nil {
			walkExpr(s.Where, fn)
		}
		for i := range s.GroupBy {
			walkExpr(&s.GroupBy[i], fn)
		}
		if s.Having != nil {
			walkExpr(s.Having, fn)
		}
		for i := range s.OrderBy {
			walkExpr(&s.OrderBy[i].Expr, fn)
		}
		if s.Limit != nil {
			walkExpr(&s.Limit.Count, fn)
			walkExpr(&s.Limit.Offset, fn)
		}
		for i := range s.Joins {
			if s.Joins[i].On != nil {
				walkExpr(s.Joins[i].On, fn)
			}
		}
	case *ast.InsertStmt:
		for i := range s.Values {
			for j := range s.Values[i] {
				walkExpr(&s.Values[i][j], fn)
			}
		}
		if s.Select != nil {
			walkStmt(s.Select, fn)
		}
	case *ast.UpdateStmt:
		for i := range s.Sets {
			walkExpr(&s.Sets[i].Val, fn)
		}
		if s.Where != nil {
			walkExpr(s.Where, fn)
		}
	case *ast.DeleteStmt:
		if s.Where != nil {
			walkExpr(s.Where, fn)
		}
	}
}

func walkExpr(e *ast.Expr, fn func(*ast.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	if e.Left != nil {
		walkExpr(e.Left, fn)
	}
	if e.Right != nil {
		walkExpr(e.Right, fn)
	}
	for i := range e.Args {
		walkExpr(&e.Args[i], fn)
	}
	for i := range e.Items {
		walkExpr(&e.Items[i], fn)
	}
	if e.Stmt != nil {
		walkStmt(e.Stmt, fn)
	}
	if e.Low != nil {
		walkExpr(e.Low, fn)
	}
	if e.High != nil {
		walkExpr(e.High, fn)
	}
}

// ResolveRunnerParams builds a typed RunnerParam list from qb.Prep.Params (SQL-level params,
// which may contain duplicates) and resolves types from q.Params and models.
func ResolveRunnerParams(qb QueryBuilt, models map[string]ModelDef) []engines.RunnerParam {
	q := qb.Q
	var out []engines.RunnerParam
	// Track occurrence counts to deduplicate names
	seen := map[string]int{}

	for _, ref := range qb.Prep.Params {
		var paramType string
		var baseName string
		if ref.IsField {
			baseName = ref.Param + "_" + ToPascal(ref.Field)
			// Resolve struct field type from models
			paramType = resolveFieldType(q, ref.Param, ref.Field, models)
		} else {
			baseName = ref.Param
			paramType = resolveParamType(q, ref.Param)
		}

		// Deduplicate names
		name := baseName
		count := seen[baseName]
		if count > 0 {
			name = fmt.Sprintf("%s_%d", baseName, count)
		}
		seen[baseName]++

		out = append(out, engines.RunnerParam{Name: name, Type: paramType})
	}
	return out
}

// resolveParamType finds the Go type for a simple param by name.
func resolveParamType(q QueryDef, name string) string {
	for _, p := range q.Params {
		if p.Name == name {
			return p.Type
		}
	}
	return "any"
}

// resolveFieldType finds the Go type for a struct field access (param.field).
func resolveFieldType(q QueryDef, paramName, fieldName string, models map[string]ModelDef) string {
	// Find the struct param type
	var structType string
	for _, p := range q.Params {
		if p.Name == paramName {
			structType = p.Type
			break
		}
	}
	if structType == "" {
		return "any"
	}
	// Look up the field type from the model definition
	if mdl, ok := models[structType]; ok {
		for _, f := range mdl.Fields {
			if f.Name == ToPascal(fieldName) {
				return f.Type
			}
		}
	}
	return "any"
}

// DetermineRunnerKind maps a query's mode + statement type to a RunnerKind.
func DetermineRunnerKind(qb QueryBuilt) engines.RunnerKind {
	q := qb.Q

	switch q.Mode {
	case "exec":
		return engines.RunnerExec
	case "execrows":
		return engines.RunnerExecRows
	}

	// For :one / :many, check if the statement has RETURNING
	if HasReturningStmt(qb.Stmt) {
		// Single-column RETURNING + scalar type → RunnerReturningScalar
		if len(qb.Prep.Returning) == 1 && q.IsScalar && !qb.Prep.HasStar {
			return engines.RunnerReturningScalar
		}
		// Otherwise fall through to normal exec
		return engines.RunnerExec
	}

	switch q.Mode {
	case "one":
		return engines.RunnerQueryOne
	case "many":
		return engines.RunnerQueryMany
	}
	return engines.RunnerQueryOne
}

// IsInsertReturningStmt checks if the statement is an INSERT (for RETURNING purposes).
func IsInsertReturningStmt(stmt ast.Statement) bool {
	_, ok := stmt.(*ast.InsertStmt)
	return ok
}

// HasReturningStmt checks if the AST statement has a RETURNING clause.
func HasReturningStmt(stmt ast.Statement) bool {
	if s, ok := stmt.(*ast.InsertStmt); ok {
		return len(s.Returning) > 0
	}
	return false
}

// -------------------- AST Formatting --------------------

// formatStmt produces a Go source string for a Statement AST literal.
func formatStmt(stmt ast.Statement) string {
	var b strings.Builder

	switch s := stmt.(type) {
	case *ast.SelectStmt:
		b.WriteString("&ast.SelectStmt{\n")
		if s.Distinct {
			b.WriteString("\tDistinct: true,\n")
		}
		b.WriteString("\tColumns: []ast.Expr{\n")
		for _, c := range s.Columns {
			fmt.Fprintf(&b, "\t\t%s,\n", formatExpr(c))
		}
		b.WriteString("\t},\n")
		fmt.Fprintf(&b, "\tFrom: %s,\n", formatTableRef(s.From))
		if len(s.Joins) > 0 {
			b.WriteString("\tJoins: []ast.JoinClause{\n")
			for _, j := range s.Joins {
				fmt.Fprintf(&b, "\t\t%s,\n", formatJoin(j))
			}
			b.WriteString("\t},\n")
		}
		if s.Where != nil {
			fmt.Fprintf(&b, "\tWhere: ast.Ptr(%s),\n", formatExpr(*s.Where))
		}
		if len(s.GroupBy) > 0 {
			b.WriteString("\tGroupBy: []ast.Expr{\n")
			for _, gb := range s.GroupBy {
				fmt.Fprintf(&b, "\t\t%s,\n", formatExpr(gb))
			}
			b.WriteString("\t},\n")
		}
		if s.Having != nil {
			fmt.Fprintf(&b, "\tHaving: ast.Ptr(%s),\n", formatExpr(*s.Having))
		}
		if len(s.OrderBy) > 0 {
			b.WriteString("\tOrderBy: []ast.OrderClause{\n")
			for _, o := range s.OrderBy {
				fmt.Fprintf(&b, "\t\t{Expr: %s, Desc: %t},\n", formatExpr(o.Expr), o.Desc)
			}
			b.WriteString("\t},\n")
		}
		if s.Limit != nil {
			b.WriteString("\tLimit: &ast.LimitClause{\n")
			fmt.Fprintf(&b, "\t\tCount: %s,\n", formatExpr(s.Limit.Count))
			if s.Limit.Offset.Kind != 0 {
				fmt.Fprintf(&b, "\t\tOffset: %s,\n", formatExpr(s.Limit.Offset))
			}
			b.WriteString("\t},\n")
		}
		b.WriteString("}")

	case *ast.InsertStmt:
		b.WriteString("&ast.InsertStmt{\n")
		fmt.Fprintf(&b, "\tTable: %s,\n", formatTableRef(s.Table))
		if len(s.Columns) > 0 {
			b.WriteString("\tColumns: []string{")
			for i, c := range s.Columns {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", c)
			}
			b.WriteString("},\n")
		}
		if s.Select != nil {
			fmt.Fprintf(&b, "\tSelect: %s,\n", formatStmt(s.Select))
		} else if len(s.Values) > 0 {
			b.WriteString("\tValues: [][]ast.Expr{\n")
			for _, row := range s.Values {
				b.WriteString("\t\t{")
				for i, v := range row {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(formatExpr(v))
				}
				b.WriteString("},\n")
			}
			b.WriteString("\t},\n")
		}
		if s.Returning != nil {
			b.WriteString("\tReturning: []string{")
			for i, r := range s.Returning {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", r)
			}
			b.WriteString("},\n")
		}
		b.WriteString("}")

	case *ast.UpdateStmt:
		b.WriteString("&ast.UpdateStmt{\n")
		fmt.Fprintf(&b, "\tTable: %s,\n", formatTableRef(s.Table))
		b.WriteString("\tSets: []ast.SetClause{\n")
		for _, set := range s.Sets {
			fmt.Fprintf(&b, "\t\t{Col: %q, Val: %s},\n", set.Col, formatExpr(set.Val))
		}
		b.WriteString("\t},\n")
		if s.Where != nil {
			fmt.Fprintf(&b, "\tWhere: ast.Ptr(%s),\n", formatExpr(*s.Where))
		}
		b.WriteString("}")

	case *ast.DeleteStmt:
		b.WriteString("&ast.DeleteStmt{\n")
		fmt.Fprintf(&b, "\tTable: %s,\n", formatTableRef(s.Table))
		if s.Where != nil {
			fmt.Fprintf(&b, "\tWhere: ast.Ptr(%s),\n", formatExpr(*s.Where))
		}
		b.WriteString("}")
	}

	return b.String()
}

func formatExpr(e ast.Expr) string {
	switch e.Kind {
	case ast.ExprCol:
		return fmt.Sprintf("ast.Col(%q)", e.Col)
	case ast.ExprParam:
		return fmt.Sprintf("ast.Param(%q)", e.Param)
	case ast.ExprLiteral:
		if e.Val == nil {
			return "ast.Lit(nil)"
		}
		s, ok := e.Val.(string)
		if !ok {
			return fmt.Sprintf("ast.Lit(%v)", e.Val)
		}
		return fmt.Sprintf("ast.Lit(%q)", s)
	case ast.ExprStar:
		return "ast.Star()"
	case ast.ExprBinary:
		return formatBinary(e)
	case ast.ExprUnary:
		return fmt.Sprintf("*ast.Not(%s)", formatExpr(*e.Left))
	case ast.ExprIsNull:
		return fmt.Sprintf("*ast.IsNull(%s)", formatExpr(*e.Left))
	case ast.ExprNotNull:
		return fmt.Sprintf("*ast.NotNull(%s)", formatExpr(*e.Left))
	case ast.ExprCall:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = formatExpr(a)
		}
		return fmt.Sprintf("ast.Call(%q, %s)", e.Name, strings.Join(args, ", "))
	case ast.ExprIn:
		if e.Stmt != nil {
			return fmt.Sprintf("*ast.InSubquery(%s, %s)",
				formatExpr(*e.Left), formatStmt(e.Stmt))
		}
		items := make([]string, len(e.Items))
		for i, item := range e.Items {
			items[i] = formatExpr(item)
		}
		return fmt.Sprintf("*ast.In(%s, %s)", formatExpr(*e.Left), strings.Join(items, ", "))
	case ast.ExprBetween:
		return fmt.Sprintf("*ast.Between(%s, %s, %s)",
			formatExpr(*e.Left), formatExpr(*e.Low), formatExpr(*e.High))
	case ast.ExprSubquery:
		return fmt.Sprintf("ast.Subquery(%s)", formatStmt(e.Stmt))
	case ast.ExprList:
		items := make([]string, len(e.Items))
		for i, item := range e.Items {
			items[i] = formatExpr(item)
		}
		return fmt.Sprintf("ast.List(%s)", strings.Join(items, ", "))
	default:
		return "ast.Expr{}"
	}
}

func formatBinary(e ast.Expr) string {
	left := formatExpr(*e.Left)
	right := formatExpr(*e.Right)
	switch e.Op {
	case "AND":
		return fmt.Sprintf("*ast.And(%s, %s)", left, right)
	case "OR":
		return fmt.Sprintf("*ast.Or(%s, %s)", left, right)
	case "=":
		return fmt.Sprintf("*ast.Eq(%s, %s)", left, right)
	case "<>", "!=":
		return fmt.Sprintf("*ast.Ne(%s, %s)", left, right)
	case "<":
		return fmt.Sprintf("*ast.Lt(%s, %s)", left, right)
	case ">":
		return fmt.Sprintf("*ast.Gt(%s, %s)", left, right)
	case "<=":
		return fmt.Sprintf("*ast.Le(%s, %s)", left, right)
	case ">=":
		return fmt.Sprintf("*ast.Ge(%s, %s)", left, right)
	case "like", "LIKE":
		return fmt.Sprintf("*ast.Like(%s, %s)", left, right)
	case "not like":
		return fmt.Sprintf("*ast.Not(*ast.Like(%s, %s))", left, right)
	default:
		return fmt.Sprintf("ast.Expr{Kind: ast.ExprBinary, Op: %q, Left: ast.Ptr(%s), Right: ast.Ptr(%s)}",
			e.Op, left, right)
	}
}

func formatTableRef(t ast.TableRef) string {
	return fmt.Sprintf("ast.TableRef{Name: %q, Alias: %q}", t.Name, t.Alias)
}

func formatJoin(j ast.JoinClause) string {
	jt := "ast.InnerJoin"
	switch j.Type {
	case ast.LeftJoin:
		jt = "ast.LeftJoin"
	case ast.RightJoin:
		jt = "ast.RightJoin"
	case ast.CrossJoin:
		jt = "ast.CrossJoin"
	}
	if j.On != nil {
		return fmt.Sprintf("{Type: %s, Table: %s, On: ast.Ptr(%s)}",
			jt, formatTableRef(j.Table), formatExpr(*j.On))
	}
	return fmt.Sprintf("{Type: %s, Table: %s}", jt, formatTableRef(j.Table))
}

// -------------------- scan / column helpers --------------------

// resolveAliases replaces vitess column names with aliases from raw SQL.
func resolveAliases(rawSQL string, cols []string) []string {
	normalized := strings.ReplaceAll(rawSQL, "\n", " ")
	upper := strings.ToUpper(normalized)
	selIdx := strings.Index(upper, "SELECT ")
	if selIdx < 0 {
		return cols
	}
	fromIdx := strings.Index(upper[selIdx:], " FROM ")
	if fromIdx < 0 {
		return cols
	}
	selClause := normalized[selIdx+7 : selIdx+fromIdx]

	parts := SplitTopLevel(selClause, ',')
	aliases := make([]string, len(parts))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		asIdx := strings.LastIndex(strings.ToUpper(part), " AS ")
		if asIdx >= 0 {
			alias := strings.TrimSpace(part[asIdx+4:])
			alias = strings.Trim(alias, "\"`'")
			aliases[i] = alias
		}
	}

	out := make([]string, len(cols))
	for i, c := range cols {
		if i < len(aliases) && aliases[i] != "" {
			out[i] = aliases[i]
		} else {
			out[i] = c
		}
	}
	return out
}

// SplitTopLevel splits by separator, respecting parentheses.
func SplitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// resultColumns returns SQL result column names from a Statement.
func resultColumns(stmt ast.Statement, prep *SQLPrep) []string {
	switch s := stmt.(type) {
	case *ast.SelectStmt:
		var cols []string
		for _, c := range s.Columns {
			if c.Kind == ast.ExprStar {
				return nil // unknown
			}
			cols = append(cols, columnName(c))
		}
		return cols
	case *ast.InsertStmt:
		if prep.HasStar {
			return nil
		}
		return s.Returning
	case *ast.UpdateStmt:
		return s.Returning
	case *ast.DeleteStmt:
		return s.Returning
	}
	return nil
}

// columnName extracts the column name from a SELECT expression, stripping table qualifiers.
func columnName(e ast.Expr) string {
	if e.Alias != "" {
		return e.Alias
	}
	var col string
	switch e.Kind {
	case ast.ExprCol:
		col = e.Col
	case ast.ExprCall:
		col = e.Name
	}
	if dot := strings.LastIndexByte(col, '.'); dot >= 0 {
		col = col[dot+1:]
	}
	return col
}

// -------------------- Helpers --------------------

// ScalarZero returns the zero value for a scalar type.
func ScalarZero(t string) string {
	switch t {
	case "bool":
		return "false"
	case "string":
		return `""`
	default:
		return "0"
	}
}

// LowerFirst lowercases the first character of a string.
func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}

// ToSnake converts a PascalCase string to snake_case.
func ToSnake(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if (prev >= 'a' && prev <= 'z') || (next >= 'a' && next <= 'z') {
				b.WriteByte('_')
			}
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// ToPascal converts a snake_case string to PascalCase.
func ToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		if strings.EqualFold(p, "id") {
			parts[i] = "ID"
		} else {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// SortedKeys returns model names in sorted order.
func SortedKeys(m map[string]ModelDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// NullableScanType returns the sql.Null* type name for non-pointer types that need nullable scanning.
func NullableScanType(goType string) string {
	switch goType {
	case "string":
		return "String"
	case "time.Time":
		return "Time"
	}
	return ""
}

// FilesStem extracts the stem from a parsed file.
func FilesStem(f *ParsedFile) string {
	if len(f.Queries) == 0 {
		return "unknown"
	}
	src := f.Queries[0].Src
	base := filepath.Base(src)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".sql"), ".sqlgen")
	return strings.ReplaceAll(base, ".", "_")
}

// ToLowerCamel converts snake_case to lowerCamelCase.
func ToLowerCamel(s string) string {
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
