package mssql

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.MSSQL} }

var _ engines.Engine = (*Generator)(nil)

func (g *Generator) Name() string { return "mssql" }

const dialectSuffixMSSQL = "MSSQL"

func (g *Generator) GenFile(stem string, specs []engines.RunnerSpec) string {
	var b strings.Builder

	for _, spec := range specs {
		sql := g.renderSQL(spec)
		g.writeRunner(&b, spec, sql, dialectSuffixMSSQL)
	}

	camelStem := toCamel(stem)
	b.WriteString("\ntype ")
	b.WriteString(camelStem)
	b.WriteString("RunnerFactoryMSSQL struct {}\n\n")

	for _, spec := range specs {
		runnerType := lowerFirst(spec.Query) + "Runner"
		b.WriteString("\nfunc (f *")
		b.WriteString(camelStem)
		b.WriteString("RunnerFactoryMSSQL) new")
		b.WriteString(spec.Query)
		b.WriteString("(db *sql.DB) ")
		b.WriteString(runnerType)
		b.WriteString(" { return &")
		b.WriteString(spec.Name)
		b.WriteString(dialectSuffixMSSQL)
		b.WriteString("{db: db} }\n")
	}
	b.WriteString("\n")

	return b.String()
}

func (g *Generator) renderSQL(spec engines.RunnerSpec) string {
	// Check for INSERT with ON CONFLICT — needs MERGE
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok && ins.OnConflict != nil {
		return g.renderMerge(ins)
	}

	sql := g.d.Render(spec.Stmt)

	// RETURNING → OUTPUT
	sql = mssqlReturningToOutput(sql)

	// NOW() → GETDATE()
	sql = strings.ReplaceAll(sql, "now()", "GETDATE()")

	// ILIKE → LOWER(x) LIKE LOWER(y)
	if spec.HasILIKE {
		sql = mssqlConvertILIKE(sql)
	}

	// Strip FROM dual (Oracle-ism from vitess)
	sql = strings.ReplaceAll(sql, " FROM dual", "")

	return engines.QuoteIdent(sql, "mssql")
}

// ---------------------------------------------------------------------------
// MERGE (ON CONFLICT → MERGE)
// ---------------------------------------------------------------------------

func (g *Generator) renderMerge(ins *ast.InsertStmt) string {
	table := ins.Table.Name
	cols := ins.Columns
	oc := ins.OnConflict

	// Render INSERT without OnConflict to get parameterized VALUES
	cleanIns := &ast.InsertStmt{
		Table:   ins.Table,
		Columns: ins.Columns,
		Values:  ins.Values,
	}
	rendered := g.d.Render(cleanIns)

	// Extract the VALUES part: everything after "VALUES "
	valuesStr := ""
	upper := strings.ToUpper(rendered)
	if idx := strings.Index(upper, "VALUES "); idx >= 0 {
		valuesStr = rendered[idx+7:] // len("VALUES ") = 7
	}

	var b strings.Builder

	// MERGE INTO table AS target
	b.WriteString("MERGE INTO ")
	b.WriteString(table)
	b.WriteString(" AS target\n")

	// USING (VALUES (...)) AS src (col1, col2, ...)
	b.WriteString("USING (VALUES ")
	b.WriteString(strings.TrimSpace(valuesStr))
	b.WriteString(") AS src (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(")\n")

	// ON target.col = src.col
	b.WriteString("ON ")
	for i, c := range oc.Columns {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString("target.")
		b.WriteString(c)
		b.WriteString(" = src.")
		b.WriteString(c)
	}
	b.WriteString("\n")

	// WHEN NOT MATCHED THEN INSERT
	b.WriteString("WHEN NOT MATCHED THEN INSERT (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(") VALUES (")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("src.")
		b.WriteString(c)
	}
	b.WriteString(")")

	// WHEN MATCHED THEN UPDATE SET (or omit for DO NOTHING)
	if oc.DoUpdate {
		b.WriteString("\nWHEN MATCHED THEN UPDATE SET ")
		for i, s := range oc.Sets {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(s.Col)
			b.WriteString(" = src.")
			// SET values are ExprCol references like "@name" → strip @ → "name"
			colName := strings.TrimPrefix(s.Val.Col, "@")
			b.WriteString(colName)
		}
	}

	// RETURNING → OUTPUT INSERTED.xxx
	if len(ins.Returning) > 0 {
		b.WriteString("\nOUTPUT ")
		b.WriteString(mssqlOutputCols(ins.Returning, false))
	}

	b.WriteByte(';')

	sql := b.String()

	// NOW() → GETDATE()
	sql = strings.ReplaceAll(sql, "now()", "GETDATE()")

	return engines.QuoteIdent(sql, "mssql")
}

// ---------------------------------------------------------------------------
// RETURNING → OUTPUT
// ---------------------------------------------------------------------------

var (
	reInsertReturning = regexp.MustCompile(`(?i)^(INSERT\s+INTO\s+\S+(?:\s+\([^)]+\))?)\s+VALUES\s+(.+?)\s+RETURNING\s+(.+)$`)
	reUpdateReturning = regexp.MustCompile(`(?i)^(UPDATE\s+\S+\s+SET\s+.+?)\s+WHERE\s+(.+?)\s+RETURNING\s+(.+)$`)
	reDeleteReturning = regexp.MustCompile(`(?i)^(DELETE\s+FROM\s+\S+)\s+WHERE\s+(.+?)\s+RETURNING\s+(.+)$`)
	reILIKE           = regexp.MustCompile(`(\w+)\s+LIKE\s+(@p\d+)`)
)

func mssqlReturningToOutput(sql string) string {
	// INSERT ... VALUES ... RETURNING cols
	if m := reInsertReturning.FindStringSubmatch(sql); m != nil {
		cols := mssqlOutputCols(strings.Split(m[3], ","), false)
		return fmt.Sprintf("%s OUTPUT %s VALUES %s", strings.TrimRight(m[1], " "), cols, m[2])
	}

	// UPDATE ... WHERE ... RETURNING cols
	if m := reUpdateReturning.FindStringSubmatch(sql); m != nil {
		cols := mssqlOutputCols(strings.Split(m[3], ","), false)
		return fmt.Sprintf("%s OUTPUT %s WHERE %s", strings.TrimRight(m[1], " "), cols, m[2])
	}

	// DELETE ... WHERE ... RETURNING cols
	if m := reDeleteReturning.FindStringSubmatch(sql); m != nil {
		cols := mssqlOutputCols(strings.Split(m[3], ","), true) // true = DELETED
		return fmt.Sprintf("%s OUTPUT %s WHERE %s", m[1], cols, m[2])
	}

	return engines.QuoteIdent(sql, "mssql")
}

// mssqlOutputCols transforms column names for OUTPUT: "id" → "INSERTED.id"
func mssqlOutputCols(cols []string, isDelete bool) string {
	prefix := "INSERTED"
	if isDelete {
		prefix = "DELETED"
	}

	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if c == "*" {
			parts = append(parts, prefix+".*")
		} else {
			parts = append(parts, prefix+"."+c)
		}
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// ILIKE → LOWER LIKE LOWER
// ---------------------------------------------------------------------------

func mssqlConvertILIKE(sql string) string {
	return reILIKE.ReplaceAllString(sql, `LOWER($1) LIKE LOWER($2)`)
}

// ---------------------------------------------------------------------------
// Runner helpers
// ---------------------------------------------------------------------------

func (g *Generator) writeRunner(b *strings.Builder, spec engines.RunnerSpec, sql, suffix string) {
	constName := spec.Name + "Const" + suffix
	runnerType := lowerFirst(spec.Query) + "Runner"
	sig := spec.ParamSignature()
	names := spec.ParamNames()

	fmt.Fprintf(b, "const %s = `%s`\n\n", constName, sql)
	fmt.Fprintf(b, "type %s struct {\n	stmt *sql.Stmt\n	db   *sql.DB\n}\n\n", spec.Name+suffix)

	closeAndTx := fmt.Sprintf(`
func (r *%[1]s) close() error { if r.stmt != nil { return r.stmt.Close() }; return nil }
func (r *%[1]s) withTx(tx *sql.Tx) %[2]s { return &%[1]s{stmt: tx.Stmt(r.stmt)} }
`, spec.Name+suffix, runnerType)

	switch spec.Kind {
	case engines.RunnerQueryOne:
		fmt.Fprintf(b, `func (r *%[1]s) query(ctx context.Context%[2]s) (*sql.Row, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.QueryRowContext(ctx%[4]s), nil
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)

	case engines.RunnerQueryMany:
		fmt.Fprintf(b, `func (r *%[1]s) query(ctx context.Context%[2]s) (*sql.Rows, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.QueryContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)

	case engines.RunnerExec, engines.RunnerExecRows:
		fmt.Fprintf(b, `func (r *%[1]s) exec(ctx context.Context%[2]s) (sql.Result, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.ExecContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)

	case engines.RunnerReturningScalar:
		fmt.Fprintf(b, `func (r *%[1]s) execReturning(ctx context.Context%[2]s) (int64, error) {
	var id int64
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return 0, err } }
	row := r.stmt.QueryRowContext(ctx%[4]s)
	if err := row.Scan(&id); err != nil { return 0, err }
	return id, nil
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)
	}
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

func toPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func lowerFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
