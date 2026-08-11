// Package gen provides Go code generation for sqlgen.
package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/meta"
)

// queryBuilt is an alias for meta.QueryBuilt.
type queryBuilt = meta.QueryBuilt

// Generator produces Go code from parsed .sql files.
type Generator struct {
	PkgPath     string                    // output directory
	PkgName     string                    // Go package name (from -- package:)
	Tags        []string                  // struct tags to generate (json, yaml)
	EngineNames []string                  // target dialect engine names
	EngineMap   map[string]engines.Engine // engine name → engine instance
	Files       []*meta.ParsedFile

	// models holds all collected models (set during generate())
	models map[string]meta.ModelDef

	// built holds pre-built query data for each file
	built []*fileBuilt
}

type fileBuilt struct {
	pf      *meta.ParsedFile
	queries []queryBuilt
}

// BuildRunnerSpecs builds RunnerSpecs and Models for a meta.ParsedFile.
// Exported for non-Go language generators (e.g. Java).
func (g *Generator) BuildRunnerSpecs(f *meta.ParsedFile) ([]engines.RunnerSpec, []engines.Model, error) {
	files := make([]meta.ParsedFile, len(g.Files))
	for i, pf := range g.Files {
		files[i] = *pf
	}
	return meta.BuildRunnerSpecs(files, f)
}

// generate produces all output files.
func (g *Generator) Generate() error {
	// Collect all models from all files
	models := collectModels(g.Files)
	g.models = models // store for param type resolution later

	// Build all queries
	for _, f := range g.Files {
		fb := &fileBuilt{pf: f}
		for _, q := range f.Queries {
			qb, err := meta.BuildQuery(q)
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
			if qb.Q.IsScalar || qb.Q.Mode == "exec" || len(qb.Columns) == 0 {
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
		for _, engName := range g.EngineNames {
			if err := g.writeEngineFile(fb, engName); err != nil {
				return err
			}
		}
	}

	return nil
}

// collectModels gathers all models, deduplicates.
func collectModels(files []*meta.ParsedFile) map[string]meta.ModelDef {
	out := map[string]meta.ModelDef{}
	for _, f := range files {
		for _, m := range f.Models {
			out[m.Name] = m
		}
	}
	return out
}

// -------------------- models.go --------------------

func (g *Generator) writeModels(models map[string]meta.ModelDef) error {
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
			b.WriteString("\t\"time\"\n")
		}
		if needsJSON {
			b.WriteString("\t\"encoding/json\"\n")
		}
		b.WriteString(")\n\n")
	}

	names := meta.SortedKeys(models)
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

	return os.WriteFile(filepath.Join(g.PkgPath, "models.go"), []byte(b.String()), 0644)
}

// -------------------- <name>.go (framework file) --------------------

func (g *Generator) writeFrameworkFile(fb *fileBuilt) error {
	stem := g.fileStem(fb.pf)
	baseName := baseFileName(stem)
	path := filepath.Join(g.PkgPath, baseName+".go")
	camelName := meta.ToLowerCamel(baseName)

	var b strings.Builder
	g.writeHeader(&b)

	// Determine imports
	needsTime := false
	for _, qb := range fb.queries {
		for _, p := range qb.Q.Params {
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
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"database/sql\"\n")
	// Check if time is needed
	needsTime = false
	for _, qb := range fb.queries {
		for _, p := range qb.Q.Params {
			if p.Type == "time.Time" || p.Type == "*time.Time" {
				needsTime = true
				break
			}
		}
		if needsTime {
			break
		}
	}
	if needsTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString(")\n\n")

	// ── Runner interfaces ──
	b.WriteString("// ── Runner interfaces ──\n\n")
	for _, qb := range fb.queries {
		runnerName := meta.LowerFirst(qb.Q.Name) + "Runner"
		kind := meta.DetermineRunnerKind(qb)
		params := meta.ResolveRunnerParams(qb, g.models)
		b.WriteString("type ")
		b.WriteString(runnerName)
		b.WriteString(" interface {\n")
		switch kind {
		case engines.RunnerQueryOne:
			b.WriteString("\tquery(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (*sql.Row, error)\n")
		case engines.RunnerQueryMany:
			b.WriteString("\tquery(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (*sql.Rows, error)\n")
		case engines.RunnerExec, engines.RunnerExecRows:
			b.WriteString("\texec(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (sql.Result, error)\n")
		case engines.RunnerReturningScalar:
			b.WriteString("\texecReturning(ctx context.Context")
			g.runnerParamSignature(&b, params)
			b.WriteString(") (int64, error)\n")
		}
		b.WriteString("\tclose() error\n")
		b.WriteString("\twithTx(tx *sql.Tx) ")
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
		methodName := "new" + qb.Q.Name
		runnerName := meta.LowerFirst(qb.Q.Name) + "Runner"
		b.WriteString("\t")
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
		b.WriteString(meta.LowerFirst(qb.Q.Name))
		b.WriteString(" ")
		b.WriteString(meta.LowerFirst(qb.Q.Name) + "Runner")
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
		b.WriteString(meta.LowerFirst(qb.Q.Name))
		b.WriteString(": f.new")
		b.WriteString(qb.Q.Name)
		b.WriteString("(db),\n")
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// ── Engine constructors ──
	// Generate factory map and New function for all configured engines
	b.WriteString("var factorys = map[string]")
	b.WriteString(factoryName)
	b.WriteString("{\n")
	engineNames := g.EngineNames
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
		b.WriteString("\t\"")
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
	b.WriteString("\tf, ok := factorys[driver]\n")
	b.WriteString("\tif !ok {\n")
	b.WriteString("\t\treturn nil, fmt.Errorf(\"sqlgen: unsupported driver %q\", driver)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn new")
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
		b.WriteString(meta.LowerFirst(qb.Q.Name))
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
		fn := meta.LowerFirst(qb.Q.Name)
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
		b.WriteString(qb.Q.Name)
		b.WriteString("(ctx context.Context")
		for _, p := range qb.Q.Params {
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
func (g *Generator) writeFrameworkMethod(b *strings.Builder, qb queryBuilt, camelName, querierName string) {
	q := qb.Q
	kind := meta.DetermineRunnerKind(qb)
	fn := meta.LowerFirst(q.Name)

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
		b.WriteString("\t_, err := q.")
		b.WriteString(fn)
		b.WriteString(".exec(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")
		b.WriteString("\treturn err\n")

	case engines.RunnerExecRows:
		// :execrows — call exec, return RowsAffected
		b.WriteString("\tresult, err := q.")
		b.WriteString(fn)
		b.WriteString(".exec(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")
		b.WriteString("\tif err != nil { return 0, err }\n")
		b.WriteString("\treturn result.RowsAffected()\n")

	case engines.RunnerQueryOne:
		if q.IsScalar {
			b.WriteString("\trow, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("\tif err != nil { return ")
			b.WriteString(meta.ScalarZero(q.Return))
			b.WriteString(", err }\n")
			b.WriteString("\tvar item ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("\tif err := row.Scan(&item); err != nil { return ")
			b.WriteString(meta.ScalarZero(q.Return))
			b.WriteString(", err }\n")
			b.WriteString("\treturn item, nil\n")
		} else {
			b.WriteString("\trow, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("\tif err != nil { return nil, err }\n")
			b.WriteString("\tvar r ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			g.writeExplicitScan(b, "r", "row", "\t", qb)
			b.WriteString("\treturn &r, nil\n")
		}

	case engines.RunnerQueryMany:
		if q.IsScalar {
			b.WriteString("\trows, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("\tif err != nil { return nil, err }\n")
			b.WriteString("\tdefer rows.Close()\n")
			b.WriteString("\tvar items []")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("\tfor rows.Next() {\n")
			b.WriteString("\t\tvar item ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("\t\tif err := rows.Scan(&item); err != nil { return nil, err }\n")
			b.WriteString("\t\titems = append(items, item)\n")
			b.WriteString("\t}\n")
			b.WriteString("\treturn items, rows.Err()\n")
		} else {
			b.WriteString("\trows, err := q.")
			b.WriteString(fn)
			b.WriteString(".query(ctx")
			g.runnerCallArgs(b, qb)
			b.WriteString(")\n")
			b.WriteString("\tif err != nil { return nil, err }\n")
			b.WriteString("\tdefer rows.Close()\n")
			b.WriteString("\tvar items []*")
			b.WriteString(q.Return)
			b.WriteString("\n")
			b.WriteString("\tfor rows.Next() {\n")
			b.WriteString("\t\tvar r ")
			b.WriteString(q.Return)
			b.WriteString("\n")
			g.writeExplicitScan(b, "r", "rows", "\t\t", qb)
			b.WriteString("\t\titems = append(items, &r)\n")
			b.WriteString("\t}\n")
			b.WriteString("\treturn items, rows.Err()\n")
		}

	case engines.RunnerReturningScalar:
		// RETURNING single column (int64) — runner returns (int64, error)
		b.WriteString("\treturn q.")
		b.WriteString(fn)
		b.WriteString(".execReturning(ctx")
		g.runnerCallArgs(b, qb)
		b.WriteString(")\n")

	}

	b.WriteString("}\n\n")
}

// writeReturnType writes the return type signature for a query method.
func (g *Generator) writeReturnType(b *strings.Builder, qb queryBuilt) {
	q := qb.Q
	kind := meta.DetermineRunnerKind(qb)

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
func (g *Generator) writeExplicitScan(b *strings.Builder, varName string, scanVia string, indent string, qb queryBuilt) {
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
func (g *Generator) buildScanTargets(qb queryBuilt) []scanTargetInfo {
	q := qb.Q

	// Build column→field mapping
	colToField := map[string]string{}
	for _, fm := range q.FieldMaps {
		colToField[fm.Column] = fm.Field
	}

	// For RETURNING columns (from INSERT/UPDATE/DELETE RETURNING),
	// use the model fields directly if FieldMaps are specified, otherwise fall through
	if len(q.FieldMaps) > 0 && len(qb.Columns) == 0 {
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
	for _, col := range qb.Columns {
		if _, ok := colToField[col]; ok {
			continue
		}
		pascal := meta.ToPascal(col)
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
	for _, col := range qb.Columns {
		if field, ok := colToField[col]; ok {
			targets = append(targets, g.makeTarget(field, q.Return))
		}
	}
	return targets
}

// makeTarget creates a scanTargetInfo, detecting nullable types.
func (g *Generator) makeTarget(field, returnModel string) scanTargetInfo {
	// Discard vars
	if strings.HasPrefix(field, "_d") {
		return scanTargetInfo{Field: field}
	}
	// Check model field type for nullable scan
	if mdl, ok := g.models[returnModel]; ok {
		for _, f := range mdl.Fields {
			if f.Name == field {
				nt := meta.NullableScanType(f.Type)
				return scanTargetInfo{Field: field, NullType: nt}
			}
		}
	}
	return scanTargetInfo{Field: field}
}

// -------------------- <name>.sql.<engine>.go (engine file) --------------------

func (g *Generator) writeEngineFile(fb *fileBuilt, engName string) error {
	stem := g.fileStem(fb.pf)
	baseName := baseFileName(stem)
	path := filepath.Join(g.PkgPath, baseName+".sql."+engName+".go")

	eng, ok := g.EngineMap[engName]
	if !ok {
		return fmt.Errorf("unknown engine %q", engName)
	}

	var b strings.Builder
	g.writeHeader(&b)

	// Imports for engine file
	// Check if fmt is needed (MySQL with non-INSERT RETURNING)
	needsFmt := false
	if engName == "mysql" {
		for _, qb := range fb.queries {
			kind := meta.DetermineRunnerKind(qb)
			if kind == engines.RunnerReturningScalar && !meta.IsInsertReturningStmt(qb.Stmt) {
				needsFmt = true
				break
			}
		}
	}
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"database/sql\"\n")
	if needsFmt {
		b.WriteString("\t\"fmt\"\n")
	}
	// Check if time is needed
	needsTime := false
	for _, qb := range fb.queries {
		for _, p := range qb.Q.Params {
			if p.Type == "time.Time" || p.Type == "*time.Time" {
				needsTime = true
				break
			}
		}
		if needsTime {
			break
		}
	}
	if needsTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString(")\n\n")

	// Generate each runner implementation via engine
	var specs []engines.RunnerSpec
	for _, qb := range fb.queries {
		kind := meta.DetermineRunnerKind(qb)
		params := meta.ResolveRunnerParams(qb, g.models)
		specs = append(specs, engines.RunnerSpec{
			Name:      meta.LowerFirst(qb.Q.Name),
			Kind:      kind,
			Query:     qb.Q.Name,
			IsScalar:  qb.Q.IsScalar,
			HasILIKE:  qb.Prep.HasILIKE,
			ModelType: qb.Q.Return,
			Params:    params,
			Stmt:      qb.Stmt,
		})
	}

	impl := eng.GenFile(baseName, specs)
	b.WriteString(impl)
	b.WriteString("\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// runnerParamSignature builds a comma-separated typed parameter list for runner method signatures.
func (g *Generator) runnerParamSignature(b *strings.Builder, params []engines.RunnerParam) {
	for _, p := range params {
		b.WriteString(", ")
		b.WriteString(p.Name)
		b.WriteString(" ")
		b.WriteString(p.Type)
	}
}

// runnerCallArgs builds comma-separated argument values for calling a runner method.
// This mirrors what writeRunnerCallArgs did but generates the arg values directly.
func (g *Generator) runnerCallArgs(b *strings.Builder, qb queryBuilt) {
	for _, ref := range qb.Prep.Params {
		b.WriteString(", ")
		if ref.IsField {
			fieldName := meta.ToPascal(ref.Field)
			b.WriteString(ref.Param)
			b.WriteString(".")
			b.WriteString(fieldName)
		} else {
			b.WriteString(ref.Param)
		}
	}
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
	return meta.ToPascal(strings.ReplaceAll(stem, "-", "_"))
}

// -------------------- validation --------------------

// validateModelFields checks that all model fields are matched by SELECT columns.
func (g *Generator) validateModelFields(qb queryBuilt, models map[string]meta.ModelDef) error {
	q := qb.Q
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
	for _, col := range qb.Columns {
		if _, ok := colToField[col]; ok {
			continue
		}
		pascal := meta.ToPascal(col)
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
			q.Src, q.Line, q.Return, qb.Columns)
	}
	return nil
}

// -------------------- Helpers --------------------

// fileStem extracts the stem from a parsed file's first query source.
func (g *Generator) fileStem(f *meta.ParsedFile) string {
	return FilesStem(f)
}

// FilesStem extracts the stem from a parsed file (package-level helper).
func FilesStem(f *meta.ParsedFile) string { return meta.FilesStem(f) }

func baseFileName(stem string) string {
	return stem
}

func (g *Generator) writeHeader(b *strings.Builder) {
	b.WriteString("// Code generated by sqlgen; DO NOT EDIT.\n")
	b.WriteString("package ")
	b.WriteString(g.PkgName)
	b.WriteString("\n\n")
}

func (g *Generator) writeTags(b *strings.Builder, fieldName string) {
	if len(g.Tags) == 0 {
		return
	}
	snake := meta.ToSnake(fieldName)
	b.WriteString(" `")
	for i, tag := range g.Tags {
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
