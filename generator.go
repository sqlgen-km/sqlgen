package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

// Note: engine_registry.go provides getEngine() for engine lookup.

// generator produces Go code from parsed .sql files.
type generator struct {
	pkgPath string   // output directory
	pkgName string   // Go package name (from -- package:)
	tags    []string // struct tags to generate (json, yaml)
	engines []string // target dialect engines
	files   []*ParsedFile

	// models holds all collected models (set during generate())
	models map[string]ModelDef

	// built holds pre-built query data for each file
	built []*fileBuilt
}

type fileBuilt struct {
	pf      *ParsedFile
	queries []queryBuilt
}

type queryBuilt struct {
	q           QueryDef
	stmt        ast.Statement
	prep        *sqlPrep
	src         string // Go source for AST literal
	columnCount int    // number of SQL result columns (0 if unknown)
	columns     []string // SQL result column names (from SELECT or RETURNING)
}

// generate produces all output files.
func (g *generator) generate() error {
	// Collect all models from all files
	models := g.collectModels()
	g.models = models // store for param type resolution later

	// Build all queries
	for _, f := range g.files {
		fb := &fileBuilt{pf: f}
		for _, q := range f.Queries {
			qb, err := g.buildQuery(q)
			if err != nil {
				return fmt.Errorf("%s:%d: %w", q.Src, q.Line, err)
			}
			fb.queries = append(fb.queries, qb)
		}
		g.built = append(g.built, fb)
	}

	// 1. models.go
	if err := g.writeModels(models); err != nil {
		return err
	}

	// Validate field mappings against models + SELECT columns
	for _, fb := range g.built {
		for _, qb := range fb.queries {
			if qb.q.IsScalar || qb.q.Mode == "exec" || len(qb.columns) == 0 {
				continue
			}
			if err := g.validateModelFields(qb, models); err != nil {
				return err
			}
		}
	}

	// 2. Framework file per input file: <name>.go
	for _, fb := range g.built {
		if err := g.writeFrameworkFile(fb); err != nil {
			return err
		}
	}

	// 3. Engine files: <name>.sql.<engine>.go
	for _, fb := range g.built {
		for _, engName := range g.engines {
			if err := g.writeEngineFile(fb, engName); err != nil {
				return err
			}
		}
	}

	return nil
}

// collectModels gathers all models, deduplicates.
func (g *generator) collectModels() map[string]ModelDef {
	out := map[string]ModelDef{}
	for _, f := range g.files {
		for _, m := range f.Models {
			out[m.Name] = m
		}
	}
	return out
}

// buildQuery preprocesses and parses a query.
func (g *generator) buildQuery(q QueryDef) (queryBuilt, error) {
	prep := preprocessSQL(q.SQL)

	stmt, err := parseSQLStmt(prep.cleanedSQL)
	if err != nil {
		return queryBuilt{}, fmt.Errorf("query %q (cleaned: %q): %w", q.Name, prep.cleanedSQL, err)
	}

	// Post-process: replace placeholder "_" params with actual names from prep.params
	// The params appear in AST in the same order as they were replaced in SQL.
	replaceParams(stmt, prep.params)

	// Attach ON CONFLICT if extracted
	if prep.onConflict != nil {
		if ins, ok := stmt.(*ast.InsertStmt); ok {
			oc := &ast.OnConflict{
				Columns:  prep.onConflict.Columns,
				DoUpdate: prep.onConflict.DoUpdate,
			}
			for _, s := range prep.onConflict.Sets {
				oc.Sets = append(oc.Sets, ast.SetClause{Col: s.Col, Val: ast.Expr{Kind: ast.ExprCol, Col: s.Val}})
			}
			ins.OnConflict = oc
		}
	}

	// Detect silently dropped clauses (vitess parsing gaps)
	if strings.Contains(strings.ToUpper(q.SQL), "GROUP BY") {
		s, ok := stmt.(*ast.SelectStmt)
		if !ok || len(s.GroupBy) == 0 {
			return queryBuilt{}, fmt.Errorf("query %q: GROUP BY clause was silently dropped by parser (SQL: %q)", q.Name, q.SQL)
		}
	}

	// RETURNING validation: only INSERT with single-column RETURNING is supported
	if len(prep.returning) > 0 || prep.hasStar {
		switch s := stmt.(type) {
		case *ast.InsertStmt:
			if prep.hasStar {
				return queryBuilt{}, fmt.Errorf("query %q: RETURNING * not supported; use RETURNING col for single column", q.Name)
			}
			if len(prep.returning) > 1 {
				return queryBuilt{}, fmt.Errorf("query %q: multi-column RETURNING not supported; use a single column", q.Name)
			}
			s.Returning = prep.returning
		case *ast.UpdateStmt:
			return queryBuilt{}, fmt.Errorf("query %q: UPDATE RETURNING not supported", q.Name)
		case *ast.DeleteStmt:
			return queryBuilt{}, fmt.Errorf("query %q: DELETE RETURNING not supported", q.Name)
		}
	}

	src := formatStmt(stmt)
	cols := resultColumns(stmt, prep)
	// Use raw SQL to resolve aliases that vitess discards
	if len(cols) > 0 {
		cols = resolveAliases(q.SQL, cols)
	}

	return queryBuilt{q: q, stmt: stmt, prep: prep, src: src, columnCount: len(cols), columns: cols}, nil
}

// paramIndex tracks replacement of "_" params during AST walk.
var paramIdx int

// replaceParams walks an AST and replaces Param("_") placeholders
// with the correct names from refs, in order.
func replaceParams(stmt ast.Statement, refs []paramRef) {
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

// -------------------- models.go --------------------

func (g *generator) writeModels(models map[string]ModelDef) error {
	var b strings.Builder
	g.writeHeader(&b)

	needsTime := false
	needsJSON := false
	for _, m := range models {
		for _, f := range m.Fields {
			if f.Type == "time.Time" || f.Type == "*time.Time" {
				needsTime = true
			}
			if strings.Contains(f.Type, "json.") {
				needsJSON = true
			}
		}
	}
	if needsTime || needsJSON {
		b.WriteString("import (\n")
		if needsTime {
			b.WriteString("	\"time\"\n")
		}
		if needsJSON {
			b.WriteString("	\"encoding/json\"\n")
		}
		b.WriteString(")\n\n")
	}

	names := sortedKeys(models)
	for _, name := range names {
		m := models[name]
		b.WriteString("type ")
		b.WriteString(m.Name)
		b.WriteString(" struct {\n")
		for _, f := range m.Fields {
			b.WriteString("\t")
			b.WriteString(f.Name)
			b.WriteString(" ")
			b.WriteString(f.Type)
			g.writeTags(&b, f.Name)
			b.WriteByte('\n')
		}
		b.WriteString("}\n\n")
	}

	return os.WriteFile(filepath.Join(g.pkgPath, "models.go"), []byte(b.String()), 0644)
}

// -------------------- <name>.go (framework file) --------------------

func (g *generator) writeFrameworkFile(fb *fileBuilt) error {
	stem := g.fileStem(fb.pf)
	baseName := baseFileName(stem)
	path := filepath.Join(g.pkgPath, baseName+".go")
	camelName := toCamel(baseName)

	var b strings.Builder
	g.writeHeader(&b)

	// Determine imports
	needsTime := false
	for _, qb := range fb.queries {
		for _, p := range qb.q.Params {
			if p.Type == "time.Time" || p.Type == "*time.Time" {
				needsTime = true
				break
			}
		}
		if needsTime {
			break
		}
	}

	b.WriteString("import (\n")
	b.WriteString("	\"fmt\"\n")
	b.WriteString("	\"context\"\n")
	b.WriteString("	\"database/sql\"\n")
	// Check if time is needed
	needsTime = false
	for _, qb := range fb.queries {
		for _, p := range qb.q.Params {
			if p.Type == "time.Time" || p.Type == "*time.Time" {
				needsTime = true
				break
			}
		}
		if needsTime { break }
	}
	if needsTime {
		b.WriteString("	\"time\"\n")
	}
	b.WriteString(")\n\n")

	// ── Runner interfaces ──
	b.WriteString("// ── Runner interfaces ──\n\n")
	for _, qb := range fb.queries {
		runnerName := lowerFirst(qb.q.Name) + "Runner"
		kind := determineRunnerKind(qb)
		params := g.resolveRunnerParams(qb)
		b.WriteString("type ")
		b.WriteString(runnerName)
		b.WriteString(" interface {\n")
		switch kind {
		case engines.RunnerQueryOne:
			b.WriteString("	query(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (*sql.Row, error)\n")
		case engines.RunnerQueryMany:
			b.WriteString("	query(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (*sql.Rows, error)\n")
		case engines.RunnerExec, engines.RunnerExecRows:
			b.WriteString("	exec(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (sql.Result, error)\n")
		case engines.RunnerReturningScalar:
			b.WriteString("	execReturning(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (int64, error)\n")
		}
		b.WriteString("	close() error\n")
		b.WriteString("	withTx(tx *sql.Tx) ")
		b.WriteString(runnerName)
		b.WriteString("\n")
		b.WriteString("}\n\n")
	}

	// ── Factory interface ──
	factoryName := camelName + "RunnerFactory"
	b.WriteString("// ── Factory interface ──\n\n")
	b.WriteString("type ")
	b.WriteString(factoryName)
	b.WriteString(" interface {\n")
	for _, qb := range fb.queries {
		methodName := "new" + qb.q.Name
		runnerName := lowerFirst(qb.q.Name) + "Runner"
		b.WriteString("	")
		b.WriteString(methodName)
		b.WriteString("(db *sql.DB) ")
		b.WriteString(runnerName)
		b.WriteString("\n")
	}
	b.WriteString("}\n\n")

	// ── queries struct ──
	b.WriteString("// ── queries struct ──\n\n")
	b.WriteString("type ")
	b.WriteString(camelName)
	b.WriteString("Queries struct {\n")
	b.WriteString("\tdb *sql.DB\n")
	for _, qb := range fb.queries {
		b.WriteString("\t")
		b.WriteString(lowerFirst(qb.q.Name))
		b.WriteString(" ")
		b.WriteString(lowerFirst(qb.q.Name) + "Runner")
		b.WriteString("\n")
	}
	b.WriteString("}\n\n")

	querierName := stemToPascal(stem) + "Querier"
	b.WriteString("var _ ")
	b.WriteString(querierName)
	b.WriteString(" = (*")
	b.WriteString(camelName)
	b.WriteString("Queries)(nil)\n\n")

	// newQueries constructor
	b.WriteString("func new")
	b.WriteString(camelName)
	b.WriteString("Queries(db *sql.DB, f ")
	b.WriteString(factoryName)
	b.WriteString(") *")
	b.WriteString(camelName)
	b.WriteString("Queries {\n")
	b.WriteString("\treturn &")
	b.WriteString(camelName)
	b.WriteString("Queries{\n")
	b.WriteString("\t\tdb: db,\n")
	for _, qb := range fb.queries {
		b.WriteString("\t\t")
		b.WriteString(lowerFirst(qb.q.Name))
		b.WriteString(": f.new")
		b.WriteString(qb.q.Name)
		b.WriteString("(db),\n")
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// ── Engine constructors ──
	// Generate factory map and New function for all configured engines
	b.WriteString("var factorys = map[string]")
	b.WriteString(factoryName)
	b.WriteString("{\n")
	engineNames := g.engines
	if len(engineNames) == 0 {
		engineNames = []string{"pg"}
	}
	driverNames := map[string]string{
		"pg":     "postgres",
		"mysql":  "mysql",
		"oracle": "oracle",
		"mssql":  "sqlserver",
	}
	for _, engName := range engineNames {
		suffix := enginePascalSuffix(engName)
		b.WriteString("	\"")
		b.WriteString(driverNames[engName])
		b.WriteString("\": &")
		b.WriteString(camelName)
		b.WriteString("RunnerFactory" + suffix)
		b.WriteString("{},\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("func New(db *sql.DB, driver string) (")
	b.WriteString(querierName)
	b.WriteString(", error) {\n")
	b.WriteString("	f, ok := factorys[driver]\n")
	b.WriteString("	if !ok {\n")
	b.WriteString("		return nil, fmt.Errorf(\"sqlgen: unsupported driver %q\", driver)\n")
	b.WriteString("	}\n")
	b.WriteString("	return new")
	b.WriteString(camelName)
	b.WriteString("Queries(db, f), nil\n")
	b.WriteString("}\n\n")

	// ── Public methods ──
	b.WriteString("// ── 公共方法 ──\n\n")
	for _, qb := range fb.queries {
		g.writeFrameworkMethod(&b, qb, camelName, querierName)
	}

	// ── Close ──
	b.WriteString("func (q *")
	b.WriteString(camelName)
	b.WriteString("Queries) Close() error {\n")
	for _, qb := range fb.queries {
		b.WriteString("\tq.")
		b.WriteString(lowerFirst(qb.q.Name))
		b.WriteString(".close()\n")
	}
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")

	// ── WithTx ──
	b.WriteString("func (q *")
	b.WriteString(camelName)
	b.WriteString("Queries) WithTx(tx *sql.Tx) ")
	b.WriteString(querierName)
	b.WriteString(" {\n")
	b.WriteString("\treturn &")
	b.WriteString(camelName)
	b.WriteString("Queries{\n")
	b.WriteString("\t\tdb: q.db,\n")
	for _, qb := range fb.queries {
		fn := lowerFirst(qb.q.Name)
		b.WriteString("\t\t")
		b.WriteString(fn)
		b.WriteString(": q.")
		b.WriteString(fn)
		b.WriteString(".withTx(tx),\n")
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// ── Querier interface ──
	b.WriteString("type ")
	b.WriteString(querierName)
	b.WriteString(" interface {\n")
	for _, qb := range fb.queries {
		b.WriteString("\t")
		b.WriteString(qb.q.Name)
		b.WriteString("(ctx context.Context")
		for _, p := range qb.q.Params {
			b.WriteString(", ")
			b.WriteString(p.Name)
			b.WriteString(" ")
			b.WriteString(p.Type)
		}
		b.WriteString(") (")
		g.writeReturnType(&b, qb)
		b.WriteString(")\n")
	}
	b.WriteString("\tClose() error\n")
	b.WriteString("}\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeFrameworkMethod writes a single public method for the framework file.
func (g *generator) writeFrameworkMethod(b *strings.Builder, qb queryBuilt, camelName, querierName string) {
	q := qb.q
	kind := determineRunnerKind(qb)
	fn := lowerFirst(q.Name)

	b.WriteString("func (q *")
	b.WriteString(camelName)
	b.WriteString("Queries) ")
	b.WriteString(q.Name)
	b.WriteString("(ctx context.Context")
	for _, p := range q.Params {
		b.WriteString(", ")
		b.WriteString(p.Name)
		b.WriteString(" ")
		b.WriteString(p.Type)
	}
	b.WriteString(") (")
	g.writeReturnType(b, qb)
	b.WriteString(") {\n")

	switch kind {
	case engines.RunnerExec:
		// :exec — just call exec, return error
		b.WriteString("	_, err := q.")
		b.WriteString(fn)
		b.WriteString(".exec(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")
		b.WriteString("	return err\n")

	case engines.RunnerExecRows:
		// :execrows — call exec, return RowsAffected
		b.WriteString("	result, err := q.")
		b.WriteString(fn)
		b.WriteString(".exec(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")
		b.WriteString("	if err != nil { return 0, err }\n")
		b.WriteString("	return result.RowsAffected()\n")

	case engines.RunnerQueryOne:
		if q.IsScalar {
			b.WriteString("	row, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("	if err != nil { return ")
			b.WriteString(scalarZero(q.Return))
			b.WriteString(", err }\n")
			b.WriteString("	var item ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("	if err := row.Scan(&item); err != nil { return ")
			b.WriteString(scalarZero(q.Return))
			b.WriteString(", err }\n")
			b.WriteString("	return item, nil\n")
		} else {
			b.WriteString("	row, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("	if err != nil { return nil, err }\n")
			b.WriteString("	var r ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			g.writeExplicitScan(b, "r", "row", "	", qb)
			b.WriteString("	return &r, nil\n")
		}

	case engines.RunnerQueryMany:
		if q.IsScalar {
			b.WriteString("	rows, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("	if err != nil { return nil, err }\n")
			b.WriteString("	defer rows.Close()\n")
			b.WriteString("	var items []")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("	for rows.Next() {\n")
			b.WriteString("		var item ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("		if err := rows.Scan(&item); err != nil { return nil, err }\n")
			b.WriteString("		items = append(items, item)\n")
			b.WriteString("	}\n")
			b.WriteString("	return items, rows.Err()\n")
		} else {
			b.WriteString("	rows, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("	if err != nil { return nil, err }\n")
			b.WriteString("	defer rows.Close()\n")
			b.WriteString("	var items []*")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("	for rows.Next() {\n")
			b.WriteString("		var r ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			g.writeExplicitScan(b, "r", "rows", "		", qb)
			b.WriteString("		items = append(items, &r)\n")
			b.WriteString("	}\n")
			b.WriteString("	return items, rows.Err()\n")
		}

	case engines.RunnerReturningScalar:
		// RETURNING single column (int64) — runner returns (int64, error)
		b.WriteString("	return q.")
		b.WriteString(fn)
		b.WriteString(".execReturning(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")

	}

	b.WriteString("}\n\n")
}

// writeReturnType writes the return type signature for a query method.
func (g *generator) writeReturnType(b *strings.Builder, qb queryBuilt) {
	q := qb.q
	kind := determineRunnerKind(qb)

	switch kind {
	case engines.RunnerExec:
		b.WriteString("error")
	case engines.RunnerExecRows:
		b.WriteString("int64, error")
	case engines.RunnerReturningScalar:
		b.WriteString(q.Return + ", error")
	default:
		// QueryOne or QueryMany
		if q.Mode == "many" {
			if q.IsScalar {
				b.WriteString("[]" + q.Return + ", error")
			} else {
				b.WriteString("[]*" + q.Return + ", error")
			}
		} else {
			if q.IsScalar {
				b.WriteString(q.Return + ", error")
			} else {
				b.WriteString("*" + q.Return + ", error")
			}
		}
	}
}

// writeExplicitScan writes a rows.Scan() or row.Scan() call with model field pointers.
// scanVia is "rows" or "row".
func (g *generator) writeExplicitScan(b *strings.Builder, varName string, scanVia string, indent string, qb queryBuilt) {
	scanTargets := g.buildScanTargets(qb)

	// Declare discard vars and nullable wrappers
	nIdx := 0
	for _, t := range scanTargets {
		if strings.HasPrefix(t.Field, "_d") {
			b.WriteString(indent)
			b.WriteString("var ")
			b.WriteString(t.Field)
			b.WriteString(" interface{}\n")
		} else if t.NullType != "" {
			nIdx++
			nullName := fmt.Sprintf("_ns%d", nIdx)
			b.WriteString(indent)
			b.WriteString("var ")
			b.WriteString(nullName)
			b.WriteString(" sql.Null")
			b.WriteString(t.NullType)
			b.WriteString("\n")
		}
	}

	b.WriteString(indent)
	b.WriteString("if err := ")
	b.WriteString(scanVia)
	b.WriteString(".Scan(")

	nIdx = 0
	for i, t := range scanTargets {
		if i > 0 {
			b.WriteString(", ")
		}
		if strings.HasPrefix(t.Field, "_d") {
			b.WriteString("&")
			b.WriteString(t.Field)
		} else if t.NullType != "" {
			nIdx++
			b.WriteString("&_ns")
			b.WriteString(fmt.Sprint(nIdx))
		} else {
			b.WriteString("&")
			b.WriteString(varName)
			b.WriteString(".")
			b.WriteString(t.Field)
		}
	}
	b.WriteString("); err != nil { return nil, err }\n")

	// Assign nullable wrappers back to model fields
	nIdx = 0
	for _, t := range scanTargets {
		if t.NullType != "" {
			nIdx++
			nullName := fmt.Sprintf("_ns%d", nIdx)
			b.WriteString(indent)
			switch t.NullType {
			case "String":
				b.WriteString(fmt.Sprintf("%s.%s = %s.String\n", varName, t.Field, nullName))
			case "Byte":
				b.WriteString(fmt.Sprintf("%s.%s = []byte(%s.String)\n", varName, t.Field, nullName))
			case "Time":
				b.WriteString(fmt.Sprintf("%s.%s = %s.Time\n", varName, t.Field, nullName))
			}
		}
	}
}

// scanTargetInfo describes a single scan target — either a model field or a discard var.
type scanTargetInfo struct {
	Field    string // model field name, or _dN for discard
	NullType string // "", "String", "Byte", "Time" for nullable non-pointer fields
}

// buildScanTargets returns scan targets in SELECT column order.
func (g *generator) buildScanTargets(qb queryBuilt) []scanTargetInfo {
	q := qb.q

	// Build column→field mapping
	colToField := map[string]string{}
	for _, fm := range q.FieldMaps {
		colToField[fm.Column] = fm.Field
	}

	// For RETURNING columns (from INSERT/UPDATE/DELETE RETURNING),
	// use the model fields directly if FieldMaps are specified, otherwise fall through
	if len(q.FieldMaps) > 0 && len(qb.columns) == 0 {
		var targets []scanTargetInfo
		for _, fm := range q.FieldMaps {
			targets = append(targets, g.makeTarget(fm.Field, q.Return))
		}
		return targets
	}

	// For SELECT columns, match columns to model fields
	// Auto-match remaining columns to model fields by PascalCase
	// We need model field list — use the fields from FieldMaps
	fieldNames := map[string]bool{}
	for _, fm := range q.FieldMaps {
		fieldNames[fm.Field] = true
	}
	// Build set of valid model fields
	validFields := map[string]bool{}
	if mdl, ok := g.models[q.Return]; ok {
		for _, f := range mdl.Fields {
			validFields[f.Name] = true
		}
	}

	// Also try to match by PascalCase for columns not in FieldMaps
	discardN := 0
	for _, col := range qb.columns {
		if _, ok := colToField[col]; ok {
			continue
		}
		pascal := toPascal(col)
		// Only accept auto-match if field exists in model
		if validFields[pascal] {
			colToField[col] = pascal
		} else {
			discardN++
			colToField[col] = fmt.Sprintf("_d%d", discardN)
		}
	}

	// Generate scan targets in SELECT column order
	var targets []scanTargetInfo
	for _, col := range qb.columns {
		if field, ok := colToField[col]; ok {
			targets = append(targets, g.makeTarget(field, q.Return))
		}
	}
	return targets
}

// makeTarget creates a scanTargetInfo, detecting nullable types.
func (g *generator) makeTarget(field, returnModel string) scanTargetInfo {
	// Discard vars
	if strings.HasPrefix(field, "_d") {
		return scanTargetInfo{Field: field}
	}
	// Check model field type for nullable scan
	if mdl, ok := g.models[returnModel]; ok {
		for _, f := range mdl.Fields {
			if f.Name == field {
				nt := nullableScanType(f.Type)
				return scanTargetInfo{Field: field, NullType: nt}
			}
		}
	}
	return scanTargetInfo{Field: field}
}

// nullableScanType returns the sql.Null* type name for non-pointer types that need nullable scanning.
func nullableScanType(goType string) string {
	switch goType {
	case "string":
		return "String"
	case "[]byte":
		return "Byte"
	case "time.Time":
		return "Time"
	}
	return ""
}

// -------------------- <name>.sql.<engine>.go (engine file) --------------------

func (g *generator) writeEngineFile(fb *fileBuilt, engName string) error {
	stem := g.fileStem(fb.pf)
	baseName := baseFileName(stem)
	path := filepath.Join(g.pkgPath, baseName+".sql."+engName+".go")

	eng, err := getEngine(engName)
	if err != nil {
		return err
	}

	var b strings.Builder
	g.writeHeader(&b)

	// Imports for engine file
	// Check if fmt is needed (MySQL with non-INSERT RETURNING)
	needsFmt := false
	if engName == "mysql" {
		for _, qb := range fb.queries {
			kind := determineRunnerKind(qb)
			if kind == engines.RunnerReturningScalar && !isInsertReturningStmt(qb.stmt) {
				needsFmt = true
				break
			}
		}
	}
	b.WriteString("import (\n")
	b.WriteString("	\"context\"\n")
	b.WriteString("	\"database/sql\"\n")
	if needsFmt {
		b.WriteString("	\"fmt\"\n")
	}
	// Check if time is needed
	needsTime := false
	for _, qb := range fb.queries {
		for _, p := range qb.q.Params {
			if p.Type == "time.Time" || p.Type == "*time.Time" {
				needsTime = true
				break
			}
		}
		if needsTime { break }
	}
	if needsTime {
		b.WriteString("	\"time\"\n")
	}
	b.WriteString(")\n\n")


	// Generate each runner implementation via engine
	var specs []engines.RunnerSpec
	for _, qb := range fb.queries {
		kind := determineRunnerKind(qb)
		params := g.resolveRunnerParams(qb)
		specs = append(specs, engines.RunnerSpec{
			Name:     lowerFirst(qb.q.Name),
			Kind:     kind,
			Query:    qb.q.Name,
			IsScalar: qb.q.IsScalar,
			HasILIKE: qb.prep.hasILIKE,
			Params:   params,
			Stmt:     qb.stmt,
		})
	}

	impl := eng.GenFile(baseName, specs)
	b.WriteString(impl)
	b.WriteString("\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// resolveRunnerParams builds a typed RunnerParam list from qb.prep.params (SQL-level params,
// which may contain duplicates) and resolves types from qb.q.Params and models.
func (g *generator) resolveRunnerParams(qb queryBuilt) []engines.RunnerParam {
	q := qb.q
	var out []engines.RunnerParam
	// Track occurrence counts to deduplicate names
	seen := map[string]int{}

	for _, ref := range qb.prep.params {
		var paramType string
		var baseName string
		if ref.IsField {
			baseName = ref.Param + "_" + toPascal(ref.Field)
			// Resolve struct field type from models
			paramType = g.resolveFieldType(q, ref.Param, ref.Field)
		} else {
			baseName = ref.Param
			paramType = g.resolveParamType(q, ref.Param)
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
func (g *generator) resolveParamType(q QueryDef, name string) string {
	for _, p := range q.Params {
		if p.Name == name {
			return p.Type
		}
	}
	return "any"
}

// resolveFieldType finds the Go type for a struct field access (param.field).
func (g *generator) resolveFieldType(q QueryDef, paramName, fieldName string) string {
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
	if mdl, ok := g.models[structType]; ok {
		for _, f := range mdl.Fields {
			if f.Name == toPascal(fieldName) {
				return f.Type
			}
		}
	}
	return "any"
}

// runnerParamSignature builds a comma-separated typed parameter list for runner method signatures.
func (g *generator) runnerParamSignature(b *strings.Builder, params []engines.RunnerParam) {
	for _, p := range params {
		b.WriteString(", ")
		b.WriteString(p.Name)
		b.WriteString(" ")
		b.WriteString(p.Type)
	}
}

// runnerCallArgs builds comma-separated argument values for calling a runner method.
// This mirrors what writeRunnerCallArgs did but generates the arg values directly.
func (g *generator) runnerCallArgs(b *strings.Builder, qb queryBuilt) {
	for _, ref := range qb.prep.params {
		b.WriteString(", ")
		if ref.IsField {
			fieldName := toPascal(ref.Field)
			b.WriteString(ref.Param)
			b.WriteString(".")
			b.WriteString(fieldName)
		} else {
			b.WriteString(ref.Param)
		}
	}
}

// determineRunnerKind maps a query's mode + statement type to a RunnerKind.
func determineRunnerKind(qb queryBuilt) engines.RunnerKind {
	q := qb.q

	switch q.Mode {
	case "exec":
		return engines.RunnerExec
	case "execrows":
		return engines.RunnerExecRows
	}

	// For :one / :many, check if the statement has RETURNING
	if hasReturningStmt(qb.stmt) {
		// Single-column RETURNING + scalar type → RunnerReturningScalar
		if len(qb.prep.returning) == 1 && q.IsScalar && !qb.prep.hasStar {
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

// isInsertReturningStmt checks if the statement is an INSERT (for RETURNING purposes).
// Returns true for any InsertStmt, including RETURNING * (where Returning may be empty).
func isInsertReturningStmt(stmt ast.Statement) bool {
	_, ok := stmt.(*ast.InsertStmt)
	return ok
}

// hasReturningStmt checks if the AST statement has a RETURNING clause.
func hasReturningStmt(stmt ast.Statement) bool {
	if s, ok := stmt.(*ast.InsertStmt); ok {
		return len(s.Returning) > 0
	}
	return false
}

// enginePascalSuffix returns the PascalCase suffix for an engine name (e.g. "pg" → "PG").
func enginePascalSuffix(name string) string {
	switch name {
	case "pg":
		return "PG"
	case "mysql":
		return "MySQL"
	case "mssql":
		return "MSSQL"
	case "oracle":
		return "Oracle"
	default:
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// stemToPascal converts a file stem to PascalCase for type names.
func stemToPascal(stem string) string {
	return toPascal(strings.ReplaceAll(stem, "-", "_"))
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

	parts := splitTopLevel(selClause, ',')
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

// splitTopLevel splits by separator, respecting parentheses.
func splitTopLevel(s string, sep byte) []string {
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
func resultColumns(stmt ast.Statement, prep *sqlPrep) []string {
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
		if prep.hasStar {
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

// -------------------- validation --------------------

// validateModelFields checks that all model fields are matched by SELECT columns.
func (g *generator) validateModelFields(qb queryBuilt, models map[string]ModelDef) error {
	q := qb.q
	model, ok := models[q.Return]
	if !ok {
		return nil
	}

	colToField := map[string]string{}
	for _, fm := range q.FieldMaps {
		colToField[fm.Column] = fm.Field
	}
	fieldIdx := map[string]int{}
	for i, f := range model.Fields {
		fieldIdx[f.Name] = i
	}
	for _, col := range qb.columns {
		if _, ok := colToField[col]; ok {
			continue
		}
		pascal := toPascal(col)
		if _, ok := fieldIdx[pascal]; ok {
			colToField[col] = pascal
		}
	}

	matched := 0
	for _, f := range model.Fields {
		for _, mf := range colToField {
			if mf == f.Name {
				matched++
				break
			}
		}
	}
	if matched == 0 {
		return fmt.Errorf("%s:%d: model %q has no fields matching SELECT columns %v",
			q.Src, q.Line, q.Return, qb.columns)
	}
	return nil
}

// -------------------- Helpers --------------------

func scalarZero(t string) string {
	switch t {
	case "bool":
		return "false"
	case "string":
		return `""`
	default:
		return "0"
	}
}

func (g *generator) fileStem(f *ParsedFile) string {
	if len(f.Queries) == 0 {
		return "unknown"
	}
	src := f.Queries[0].Src
	base := filepath.Base(src)
	return strings.TrimSuffix(base, ".sql")
}

func baseFileName(stem string) string {
	return stem
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}

func (g *generator) writeHeader(b *strings.Builder) {
	b.WriteString("// Code generated by sqlgen; DO NOT EDIT.\n")
	b.WriteString("package ")
	b.WriteString(g.pkgName)
	b.WriteString("\n\n")
}

func (g *generator) writeTags(b *strings.Builder, fieldName string) {
	if len(g.tags) == 0 {
		return
	}
	snake := toSnake(fieldName)
	b.WriteString(" `")
	for i, tag := range g.tags {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(tag)
		b.WriteString(":\"")
		b.WriteString(snake)
		b.WriteString("\"")
	}
	b.WriteByte('`')
}

func toSnake(s string) string {
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

func sortedKeys(m map[string]ModelDef) []string {
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
