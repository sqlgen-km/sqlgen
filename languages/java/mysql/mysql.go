// Package mysql implements the Java/MyBatis MySQL engine.
package mysql

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/java"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.MySQL} }

var _ java.Engine = (*Generator)(nil)

func (g *Generator) Name() string       { return "mysql" }
func (g *Generator) Profile() string    { return "mysql" }
func (g *Generator) DriverName() string { return "mysql" }

func (g *Generator) GenMapper(stem string, specs []engines.RunnerSpec, modelType string) string {
	var b strings.Builder
	for _, spec := range specs {
		sql := g.renderSQL(spec)
		g.writeMethod(&b, spec, sql, modelType)
	}
	b.WriteString("\n")
	return b.String()
}

func (g *Generator) renderSQL(spec engines.RunnerSpec) string {
	sql := g.d.Render(spec.Stmt)

	// Strip RETURNING clause (MySQL doesn't support it)
	if spec.Kind == engines.RunnerReturningScalar {
		sql = returningRe.ReplaceAllString(sql, "")
	}

	// Handle ON CONFLICT → MySQL syntax
	sql = handleOnConflict(sql, spec.Stmt)

	// ILIKE → LOWER(x) LIKE LOWER(y)
	if spec.HasILIKE {
		sql = convertILIKE(sql)
	}

	// COALESCE with 2 args → IFNULL
	sql = coalesceRe.ReplaceAllString(sql, "IFNULL($1,$2)")

	sql = strings.ReplaceAll(sql, " FROM dual", "")
	return java.RenderMyBatisSQL(sql, spec.Params)
}

func (g *Generator) writeMethod(b *strings.Builder, spec engines.RunnerSpec, sql string, modelType string) {
	methodName := java.LowerFirst(spec.Query)
	annotation := stmtAnnotation(spec)
	mt := modelType
	if spec.ModelType != "" && spec.Kind != engines.RunnerReturningScalar {
		mt = spec.ModelType
	}

	switch spec.Kind {
	case engines.RunnerQueryOne, engines.RunnerQueryMany:
		fmt.Fprintf(b, "\n    @Override\n")
		fmt.Fprintf(b, "    %s(\"%s\")\n", annotation, escapeJava(sql))
		fmt.Fprintf(b, "    %s %s(", java.MethodReturnType(spec, mt), methodName)
		writeParams(b, spec)
		b.WriteString(");\n")

	case engines.RunnerExec, engines.RunnerExecRows:
		fmt.Fprintf(b, "\n    @Override\n")
		fmt.Fprintf(b, "    %s(\"%s\")\n", annotation, escapeJava(sql))
		fmt.Fprintf(b, "    %s %s(", java.MethodReturnType(spec, mt), methodName)
		writeParams(b, spec)
		b.WriteString(");\n")

	case engines.RunnerReturningScalar:
		fmt.Fprintf(b, "\n    @Override\n")
		fmt.Fprintf(b, "    @Insert(\"%s\")\n", escapeJava(sql))
		b.WriteString("    @SelectKey(statement = \"SELECT LAST_INSERT_ID()\",\n")
		b.WriteString("               keyProperty = \"id\", before = false, resultType = long.class)\n")
		fmt.Fprintf(b, "    long %s(", methodName)
		writeParams(b, spec)
		b.WriteString(");\n")
	}
}

func stmtAnnotation(spec engines.RunnerSpec) string {
	switch spec.Kind {
	case engines.RunnerQueryOne, engines.RunnerQueryMany:
		return "@Select"
	case engines.RunnerExec, engines.RunnerExecRows:
		switch spec.Stmt.(type) {
		case *ast.UpdateStmt:
			return "@Update"
		case *ast.DeleteStmt:
			return "@Delete"
		default:
			return "@Insert"
		}
	default:
		return "@Select"
	}
}

func writeParams(b *strings.Builder, spec engines.RunnerSpec) {
	for i, p := range spec.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		javaType := java.Go2JavaType(p.Type)
		fmt.Fprintf(b, "@Param(\"%s\") %s %s", p.Name, javaType, p.Name)
	}
}

func escapeJava(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// ── SQL post-processing (adapted from Go MySQL engine) ──

var returningRe = regexp.MustCompile(`\s+RETURNING\s+.+$`)

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
	return sql
}

var ilikeRe = regexp.MustCompile(`(\w+(?:\.\w+)?)\s+like\s+(\S+)`)

func convertILIKE(sql string) string {
	result := ilikeRe.ReplaceAllStringFunc(sql, func(match string) string {
		parts := ilikeRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return fmt.Sprintf("LOWER(%s) LIKE LOWER(%s)", parts[1], parts[2])
	})
	result = strings.ReplaceAll(result, " like ", " LIKE ")
	return result
}

var coalesceRe = regexp.MustCompile(`(?i)coalesce\(([^,]+),([^)]+)\)`)
