package mysql

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.MySQL} }

var _ engines.Engine = (*Generator)(nil)

func (g *Generator) Name() string { return "mysql" }

const dialectSuffixMySQL = "MySQL"

func (g *Generator) GenFile(stem string, specs []engines.RunnerSpec) string {
	var b strings.Builder

	for _, spec := range specs {
		sql := g.renderSQL(spec)
		g.writeRunner(&b, spec, sql, dialectSuffixMySQL)
	}

	camelStem := toCamel(stem)
	b.WriteString("\ntype ")
	b.WriteString(camelStem)
	b.WriteString("RunnerFactoryMySQL struct {}\n\n")

	for _, spec := range specs {
		runnerType := lowerFirst(spec.Query) + "Runner"
		b.WriteString("\nfunc (f *")
		b.WriteString(camelStem)
		b.WriteString("RunnerFactoryMySQL) new")
		b.WriteString(spec.Query)
		b.WriteString("(db *sql.DB) ")
		b.WriteString(runnerType)
		b.WriteString(" { return &")
		b.WriteString(spec.Name)
		b.WriteString(dialectSuffixMySQL)
		b.WriteString("{db: db} }\n")
	}
	b.WriteString("\n")

	return b.String()
}

// renderSQL renders the SQL for a runner spec with MySQL dialect.
func (g *Generator) renderSQL(spec engines.RunnerSpec) string {
	sql := g.d.Render(spec.Stmt)

	// Strip RETURNING clause (MySQL doesn't support it)
	if hasReturning(spec) {
		sql = stripReturning(sql)
	}

	// Handle ON CONFLICT → MySQL syntax
	sql = handleOnConflict(sql, spec.Stmt)

	// ILIKE → LOWER(x) LIKE LOWER(y)
	if spec.HasILIKE {
		sql = convertILIKE(sql)
	}

	// COALESCE with 2 args → IFNULL
	sql = convertCoalesce(sql)

	// Strip FROM dual (Oracle-ism from vitess)
	sql = strings.ReplaceAll(sql, " FROM dual", "")

	return engines.QuoteIdent(sql, "mysql")
}

// hasReturning checks if the spec has a RETURNING clause (any kind).
func hasReturning(spec engines.RunnerSpec) bool {
	return spec.Kind == engines.RunnerReturningScalar
}

// stripReturning removes the RETURNING clause from the rendered SQL.
// The renderer outputs " RETURNING col1, col2" or " RETURNING *" at the end.
var returningRe = regexp.MustCompile(`\s+RETURNING\s+.+$`)

func stripReturning(sql string) string {
	return returningRe.ReplaceAllString(sql, "")
}

// handleOnConflict converts PG-style ON CONFLICT to MySQL syntax.
// DO NOTHING → INSERT IGNORE
// DO UPDATE   → ON DUPLICATE KEY UPDATE
func handleOnConflict(sql string, stmt ast.Statement) string {
	ins, ok := stmt.(*ast.InsertStmt)
	if !ok || ins.OnConflict == nil {
		return sql
	}

	oc := ins.OnConflict
	if !oc.DoUpdate {
		// DO NOTHING → INSERT IGNORE
		sql = strings.Replace(sql, "INSERT INTO", "INSERT IGNORE INTO", 1)
		return sql
	}

	// DO UPDATE → ON DUPLICATE KEY UPDATE col = VALUES(col), ...
	var sets []string
	for _, s := range oc.Sets {
		sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", s.Col, s.Col))
	}
	sql = sql + " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	return engines.QuoteIdent(sql, "mysql")
}

// convertILIKE transforms LIKE to case-insensitive LOWER pattern for MySQL.
// Rendered SQL uses lowercase "like", e.g. "name like ?"
// We transform: col like ?  →  LOWER(col) LIKE LOWER(?)
//                col like 'x' → LOWER(col) LIKE LOWER('x')
var ilikeRe = regexp.MustCompile(`(\w+(?:\.\w+)?)\s+like\s+(\S+)`)

func convertILIKE(sql string) string {
	// Find each LIKE expression and wrap both sides with LOWER()
	// The right side is typically ? or a literal string
	result := ilikeRe.ReplaceAllStringFunc(sql, func(match string) string {
		parts := ilikeRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		left := parts[1]
		right := parts[2]
		return fmt.Sprintf("LOWER(%s) LIKE LOWER(%s)", left, right)
	})
	// Also fix the uppercase LIKE that was replaced
	result = strings.ReplaceAll(result, " like ", " LIKE ")
	return result
}

// convertCoalesce transforms 2-arg COALESCE to IFNULL for MySQL.
var coalesceRe = regexp.MustCompile(`(?i)coalesce\(([^,]+),([^)]+)\)`)

func convertCoalesce(sql string) string {
	return coalesceRe.ReplaceAllString(sql, "IFNULL($1,$2)")
}
// writeRunner generates the runner struct and methods for a spec.
func (g *Generator) writeRunner(b *strings.Builder, spec engines.RunnerSpec, sql, suffix string) {
	constName := spec.Name + "Const" + suffix
	runnerType := lowerFirst(spec.Query) + "Runner"
	sig := spec.ParamSignature()
	names := spec.ParamNames()

	// RETURNING scalar: multi-step execution for INSERT, plain exec fallback for UPDATE/DELETE.
	if spec.Kind == engines.RunnerReturningScalar {
		if isInsertReturning(spec.Stmt) {
			g.writeReturningRunner(b, spec, sql, suffix)
			return
		}
		// UPDATE/DELETE RETURNING: treat as exec (MySQL doesn't support it)
		fmt.Fprintf(b, "const %s = `%s`\n\n", constName, sql)
		fmt.Fprintf(b, "type %s struct {\n	stmt *sql.Stmt\n	db   *sql.DB\n}\n\n", spec.Name+suffix)

		closeAndTx := fmt.Sprintf(`
func (r *%[1]s) close() error { if r.stmt != nil { return r.stmt.Close() }; return nil }
func (r *%[1]s) withTx(tx *sql.Tx) %[2]s { return &%[1]s{stmt: tx.Stmt(r.stmt)} }
`, spec.Name+suffix, runnerType)
		fmt.Fprintf(b, `func (r *%[1]s) exec(ctx context.Context%[2]s) (sql.Result, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.ExecContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)
		// Also generate execReturning that delegates to exec for interface compatibility
		fmt.Fprintf(b, `func (r *%[1]s) execReturning(ctx context.Context%[2]s) (int64, error) {
	if _, err := r.exec(ctx%[3]s); err != nil { return 0, err }
	return 0, nil
}
`, spec.Name+suffix, sig, names)
		return
	}

	// Non-RETURNING cases: same pattern as PG
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
	}
}

// writeReturningRunner generates the two-statement runner for MySQL RETURNING.
// MySQL doesn't support RETURNING; we use ExecContext + SELECT LAST_INSERT_ID().
func (g *Generator) writeReturningRunner(b *strings.Builder, spec engines.RunnerSpec, sql, suffix string) {
	constName := spec.Name + "Const" + suffix
	selectConstName := spec.Name + "SelectConst" + suffix
	runnerType := lowerFirst(spec.Query) + "Runner"
	selectSQL := "SELECT LAST_INSERT_ID()"
	sig := spec.ParamSignature()
	names := spec.ParamNames()

	fmt.Fprintf(b, "const %s = `%s`\n\n", constName, sql)
	fmt.Fprintf(b, "const %s = `%s`\n\n", selectConstName, selectSQL)

	// Runner struct with TWO stmt fields
	fmt.Fprintf(b, "type %s struct {\n	execStmt  *sql.Stmt\n	queryStmt *sql.Stmt\n	db        *sql.DB\n}\n\n", spec.Name+suffix)

	// close and withTx for two-statement runner
	closeAndTx := fmt.Sprintf(`
func (r *%[1]s) close() error {
	if r.execStmt != nil { if err := r.execStmt.Close(); err != nil { return err } }
	if r.queryStmt != nil { if err := r.queryStmt.Close(); err != nil { return err } }
	return nil
}
func (r *%[1]s) withTx(tx *sql.Tx) %[2]s {
	return &%[1]s{execStmt: tx.Stmt(r.execStmt), queryStmt: tx.Stmt(r.queryStmt)}
}
`, spec.Name+suffix, runnerType)

	// RETURNING scalar: ExecContext + QueryRowContext + Scan
	fmt.Fprintf(b, `func (r *%[1]s) execReturning(ctx context.Context%[2]s) (int64, error) {
	if r.execStmt == nil { var err error; r.execStmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return 0, err } }
	if r.queryStmt == nil { var err error; r.queryStmt, err = r.db.PrepareContext(ctx, %[4]s); if err != nil { return 0, err } }
	if _, err := r.execStmt.ExecContext(ctx%[5]s); err != nil { return 0, err }
	var id int64
	row := r.queryStmt.QueryRowContext(ctx)
	if err := row.Scan(&id); err != nil { return 0, err }
	return id, nil
}
%[6]s
`, spec.Name+suffix, sig, constName, selectConstName, names, closeAndTx)
}

// isInsertReturning checks if the spec has an INSERT with RETURNING clause.
func isInsertReturning(stmt ast.Statement) bool {
	ins, ok := stmt.(*ast.InsertStmt)
	return ok && ins != nil
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

func lowerFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
