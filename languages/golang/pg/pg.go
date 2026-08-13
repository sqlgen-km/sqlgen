package pg

import (
	"fmt"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.PG} }

var _ engines.Engine = (*Generator)(nil)

func (g *Generator) Name() string { return "pg" }

const dialectSuffixPG = "PG"

func (g *Generator) GenFile(stem string, specs []engines.RunnerSpec) string {
	var b strings.Builder

	for _, spec := range specs {
		sql := g.renderSQL(spec)
		g.writeRunner(&b, spec, sql, dialectSuffixPG)
	}

	camelStem := toCamel(stem)
	b.WriteString("\ntype ")
	b.WriteString(camelStem)
	b.WriteString("RunnerFactoryPG struct {}\n\n")

	for _, spec := range specs {
		runnerType := lowerFirst(spec.Query) + "Runner"
		b.WriteString("\nfunc (f *")
		b.WriteString(camelStem)
		b.WriteString("RunnerFactoryPG) new")
		b.WriteString(spec.Query)
		b.WriteString("(db *sql.DB) ")
		b.WriteString(runnerType)
		b.WriteString(" { return &")
		b.WriteString(spec.Name)
		b.WriteString(dialectSuffixPG)
		b.WriteString("{db: db} }\n")
	}
	b.WriteString("\n")

	return b.String()
}

func (g *Generator) renderSQL(spec engines.RunnerSpec) string {
	sql := g.d.Render(spec.Stmt)
	if spec.HasILIKE {
		sql = strings.ReplaceAll(sql, " LIKE ", " ILIKE ")
	}
	// Strip FROM dual (Oracle-ism from vitess for bare SELECT without FROM)
	sql = strings.ReplaceAll(sql, " FROM dual", "")
	return engines.QuoteIdent(sql, "pg")
}

func (g *Generator) writeRunner(b *strings.Builder, spec engines.RunnerSpec, sql, suffix string) {
	constName := spec.Name + "Const" + suffix
	runnerType := lowerFirst(spec.Query) + "Runner"
	sig := spec.ParamSignature()
	names := spec.ParamArgs(func(p engines.RunnerParam) string {
		return "pq.Array(" + p.Name + ")"
	})

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
