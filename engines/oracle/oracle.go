package oracle

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.Ora} }

var _ engines.Engine = (*Generator)(nil)

func (g *Generator) Name() string { return "oracle" }

const dialectSuffixOracle = "Oracle"

// GenFile generates the complete engine file content (factory + all runners + SQL constants).
func (g *Generator) GenFile(stem string, specs []engines.RunnerSpec) string {
	var b strings.Builder

	for _, spec := range specs {
		sql := g.renderSQL(spec)
		g.writeRunner(&b, spec, sql, dialectSuffixOracle)
	}

	camelStem := toCamel(stem)
	b.WriteString("\ntype ")
	b.WriteString(camelStem)
	b.WriteString("RunnerFactoryOracle struct {}\n\n")

	for _, spec := range specs {
		runnerType := lowerFirst(spec.Query) + "Runner"
		b.WriteString("\nfunc (f *")
		b.WriteString(camelStem)
		b.WriteString("RunnerFactoryOracle) new")
		b.WriteString(spec.Query)
		b.WriteString("(db *sql.DB) ")
		b.WriteString(runnerType)
		b.WriteString(" { return &")
		b.WriteString(spec.Name)
		b.WriteString(dialectSuffixOracle)
		b.WriteString("{db: db} }\n")
	}
	b.WriteString("\n")

	return b.String()
}

var offsetFetchRe = regexp.MustCompile(`OFFSET :(\d+) ROWS FETCH NEXT :(\d+) ROWS ONLY`)

// renderSQL produces Oracle-dialect SQL from a RunnerSpec.
func (g *Generator) renderSQL(spec engines.RunnerSpec) string {
	// ON CONFLICT → MERGE (Oracle doesn't have ON CONFLICT)
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok && ins.OnConflict != nil {
		return g.renderMerge(ins, spec)
	}

	sql := g.d.Render(spec.Stmt)

	// Oracle GROUP BY + CLOB workaround: restructure LEFT JOIN + COUNT
	// into scalar subquery to avoid GROUP BY on CLOB columns.
	sql = rewriteGroupByToSubquery(sql)

	// ILIKE → LOWER(x) LIKE LOWER(y)
	if spec.HasILIKE {
		sql = transformILIKEOracle(sql)
	}

	// NOW() → SYSDATE
	sql = strings.ReplaceAll(sql, "now()", "SYSDATE")

	// TRUE/FALSE → 1/0 (Oracle doesn't have boolean literals)
	sql = replaceWord(sql, "TRUE", "1")
	sql = replaceWord(sql, "FALSE", "0")

	// RETURNING → RETURNING ... INTO :outN (Oracle requires INTO clause)
	sql = g.transformReturning(sql, spec.Stmt)

	return sql
}

// renderMerge builds an Oracle MERGE statement from an INSERT with ON CONFLICT.
//
// Example:
//
//	INSERT INTO products (sku, name, price, stock) VALUES (:1, :2, :3, 1)
//	ON CONFLICT (sku) DO UPDATE SET name = :2, price = :3
//
// becomes:
//
//	MERGE INTO products t
//	USING (SELECT :1 AS sku, :2 AS name, :3 AS price, 1 AS stock FROM dual) s
//	ON (t.sku = s.sku)
//	WHEN NOT MATCHED THEN INSERT (sku, name, price, stock) VALUES (s.sku, s.name, s.price, s.stock)
//	WHEN MATCHED THEN UPDATE SET name = s.name, price = s.price
func (g *Generator) renderMerge(ins *ast.InsertStmt, spec engines.RunnerSpec) string {
	oc := ins.OnConflict
	tableAlias := "t"
	sourceAlias := "s"

	var b strings.Builder

	// Build the USING (SELECT ... FROM dual) s clause
	var usingParts []string
	paramN := 0
	for i, col := range ins.Columns {
		if i < len(ins.Values[0]) {
			valStr := renderExprOra(ins.Values[0][i], &paramN)
			usingParts = append(usingParts, valStr+" AS "+col)
		}
	}

	// Build MERGE header
	b.WriteString("MERGE INTO ")
	b.WriteString(ins.Table.Name)
	b.WriteString(" ")
	b.WriteString(tableAlias)
	b.WriteString("\nUSING (SELECT ")
	b.WriteString(strings.Join(usingParts, ", "))
	b.WriteString(" FROM dual) ")
	b.WriteString(sourceAlias)

	// ON clause
	b.WriteString("\nON (")
	for i, col := range oc.Columns {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(fmt.Sprintf("%s.%s = %s.%s", tableAlias, col, sourceAlias, col))
	}
	b.WriteString(")")

	// WHEN NOT MATCHED THEN INSERT
	b.WriteString("\nWHEN NOT MATCHED THEN\n")
	b.WriteString("    INSERT (")
	b.WriteString(strings.Join(ins.Columns, ", "))
	b.WriteString(") VALUES (")
	for i, col := range ins.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(sourceAlias + "." + col)
	}
	b.WriteString(")")

	// WHEN MATCHED THEN UPDATE
	if oc.DoUpdate {
		b.WriteString("\nWHEN MATCHED THEN\n")
		b.WriteString("    UPDATE SET ")
		for i, set := range oc.Sets {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(set.Col)
			b.WriteString(" = ")
			b.WriteString(sourceAlias + "." + set.Col)
		}
	}

	// RETURNING clause (Oracle: RETURNING ... INTO :outN)
	if len(ins.Returning) > 0 {
		b.WriteString(" RETURNING ")
		b.WriteString(strings.Join(ins.Returning, ", "))
		var outParts []string
		for i := range ins.Returning {
			outParts = append(outParts, fmt.Sprintf(":out%d", i))
		}
		b.WriteString(" INTO ")
		b.WriteString(strings.Join(outParts, ", "))
	}

	return b.String()
}

// renderExprOra renders a single expression with Oracle :N parameter numbering.
func renderExprOra(e ast.Expr, n *int) string {
	switch e.Kind {
	case ast.ExprCol:
		return e.Col
	case ast.ExprLiteral:
		if e.Val == nil {
			return "NULL"
		}
		return fmt.Sprint(e.Val)
	case ast.ExprParam:
		*n++
		return fmt.Sprintf(":%d", *n)
	case ast.ExprCall:
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = renderExprOra(arg, n)
		}
		return e.Name + "(" + strings.Join(args, ", ") + ")"
	case ast.ExprBinary:
		needParen := e.Op == "AND" || e.Op == "OR"
		left := renderExprOra(*e.Left, n)
		right := renderExprOra(*e.Right, n)
		if needParen {
			return "(" + left + " " + e.Op + " " + right + ")"
		}
		return left + " " + e.Op + " " + right
	case ast.ExprUnary:
		return e.Op + " " + renderExprOra(*e.Left, n)
	case ast.ExprCast:
		return "CAST(" + renderExprOra(*e.Left, n) + " AS " + e.TypeName + ")"
	case ast.ExprBetween:
		return renderExprOra(*e.Left, n) + " BETWEEN " + renderExprOra(*e.Low, n) + " AND " + renderExprOra(*e.High, n)
	case ast.ExprIn:
		if e.Stmt != nil {
			return renderExprOra(*e.Left, n) + " IN (...) "
		}
		items := make([]string, len(e.Items))
		for i, item := range e.Items {
			items[i] = renderExprOra(item, n)
		}
		return renderExprOra(*e.Left, n) + " IN (" + strings.Join(items, ", ") + ")"
	case ast.ExprIsNull:
		return renderExprOra(*e.Left, n) + " IS NULL"
	case ast.ExprNotNull:
		return renderExprOra(*e.Left, n) + " IS NOT NULL"
	case ast.ExprStar:
		return "*"
	case ast.ExprList:
		items := make([]string, len(e.Items))
		for i, item := range e.Items {
			items[i] = renderExprOra(item, n)
		}
		return "(" + strings.Join(items, ", ") + ")"
	default:
		return "?"
	}
}

// transformReturning appends Oracle INTO clause to a RETURNING clause.
// PG: RETURNING col1, col2
// Oracle: RETURNING col1, col2 INTO :out0, :out1
func (g *Generator) transformReturning(sql string, stmt ast.Statement) string {
	returningCols := getReturningCols(stmt)
	if len(returningCols) == 0 {
		return sql
	}

	// RETURNING * — no INTO clause needed
	if len(returningCols) == 1 && returningCols[0] == "*" {
		return sql
	}

	// Count existing :N params to compute OUT param indices
	// (OUT params come after all IN params, but we use :out_N naming for clarity)
	var outParts []string
	for i := range returningCols {
		outParts = append(outParts, fmt.Sprintf(":out%d", i))
	}

	// Find "RETURNING col1, col2" and append " INTO :out0, :out1"
	re := regexp.MustCompile(`(?i)\s+RETURNING\s+[^;]+$`)
	match := re.FindString(sql)
	if match == "" {
		return sql
	}
	return strings.Replace(sql, match, match+" INTO "+strings.Join(outParts, ", "), 1)
}

// getReturningCols extracts the RETURNING column list from an AST statement.
func getReturningCols(stmt ast.Statement) []string {
	switch s := stmt.(type) {
	case *ast.InsertStmt:
		return s.Returning
	case *ast.UpdateStmt:
		return s.Returning
	case *ast.DeleteStmt:
		return s.Returning
	}
	return nil
}

// transformILIKEOracle converts PG-style LIKE to Oracle LOWER(x) LIKE LOWER(y).
// Pattern: <expr> LIKE <expr> → LOWER(<expr>) LIKE LOWER(<expr>)
// We match simple cases: word.word LIKE :N or word LIKE :N
// The LIKE operator is rendered lowercase by the AST, so match case-insensitively.
var likeRe = regexp.MustCompile(`(?i)(\w+(?:\.\w+)?)\s+LIKE\s+(:\w+)`)

func transformILIKEOracle(sql string) string {
	return likeRe.ReplaceAllString(sql, "LOWER($1) LIKE LOWER($2)")
}

// needsOffsetSwap checks if the spec's AST has a SELECT with LIMIT+OFFSET.
func needsOffsetSwap(spec engines.RunnerSpec) bool {
	sel, ok := spec.Stmt.(*ast.SelectStmt)
	if !ok || sel.Limit == nil {
		return false
	}
	return sel.Limit.Offset.Kind != 0
}

// namesWithSwap returns param names with last two swapped for Oracle OFFSET.
func namesWithSwap(spec engines.RunnerSpec) string {
	params := spec.Params
	if len(params) < 2 {
		return spec.ParamNames()
	}
	// Build comma-separated names with last two swapped
	var parts []string
	for i := 0; i < len(params)-2; i++ {
		parts = append(parts, ", "+params[i].Name)
	}
	parts = append(parts, ", "+params[len(params)-1].Name)  // offset (was last)
	parts = append(parts, ", "+params[len(params)-2].Name)  // limit (was second-to-last)
	return strings.Join(parts, "")
}

var groupBySubqueryRe = regexp.MustCompile(
	`(?is)^(SELECT\s+.+?),\s+(count\([^)]+\))\s+(FROM\s+\S+\s+\S+)\s+(LEFT\s+JOIN\s+(\S+)\s+(\S+)\s+ON\s+(.+?))(?:\s+(WHERE\s+.+?))?\s+(GROUP\s+BY\s+.+?)(\s+ORDER\s+BY\s+.+)?$`)

func rewriteGroupByToSubquery(sql string) string {
	m := groupBySubqueryRe.FindStringSubmatch(sql)
	if m == nil {
		return sql
	}
	// Only restructure if GROUP BY has exactly one column.
	// Multi-column GROUP BY means all non-aggregate columns are already listed.
	groupBy := strings.TrimSpace(m[9])
	if strings.Count(groupBy, ",") > 0 {
		return sql
	}
	cols := m[1]
	countExpr := m[2]
	fromMain := m[3]
	joinedTable := strings.TrimSpace(m[5])
	jAlias := strings.TrimSpace(m[6])
	onCond := strings.TrimSpace(m[7])
	whereClause := ""
	if m[8] != "" {
		whereClause = " " + strings.TrimSpace(m[8])
	}
	orderClause := strings.TrimSpace(m[10])

	subquery := fmt.Sprintf("(SELECT %s FROM %s %s WHERE %s)",
		countExpr, joinedTable, jAlias, onCond)

	result := fmt.Sprintf("%s, %s %s%s", cols, subquery, fromMain, whereClause)
	if orderClause != "" {
		result += " " + orderClause
	}
	return result
}

// replaceWord replaces whole-word occurrences of old with new in s.
func replaceWord(s, old, new string) string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
	return re.ReplaceAllString(s, new)
}

// writeRunner generates a single runner struct + methods.
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
		// go-ora binds params positionally. OFFSET renders before FETCH NEXT.
		if needsOffsetSwap(spec) {
			fmt.Fprintf(b, `func (r *%[1]s) query(ctx context.Context%[2]s) (*sql.Rows, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.QueryContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, namesWithSwap(spec), closeAndTx)
		} else {
			fmt.Fprintf(b, `func (r *%[1]s) query(ctx context.Context%[2]s) (*sql.Rows, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.QueryContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)
		}

	case engines.RunnerExec, engines.RunnerExecRows:
		fmt.Fprintf(b, `func (r *%[1]s) exec(ctx context.Context%[2]s) (sql.Result, error) {
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return nil, err } }
	return r.stmt.ExecContext(ctx%[4]s)
}
%[5]s
`, spec.Name+suffix, sig, constName, names, closeAndTx)

	case engines.RunnerReturningScalar:
		// go-ora: ExecContext + sql.Out binding
		fmt.Fprintf(b, `func (r *%[1]s) execReturning(ctx context.Context%[2]s) (int64, error) {
	var id int64
	if r.stmt == nil { var err error; r.stmt, err = r.db.PrepareContext(ctx, %[3]s); if err != nil { return 0, err } }
	if _, err := r.stmt.ExecContext(ctx%[4]s, &id); err != nil { return 0, err }
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
