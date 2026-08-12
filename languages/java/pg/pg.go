// Package pg implements the Java/MyBatis PG (PostgreSQL) engine.
package pg

import (
	"fmt"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/java"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.PG} }

var _ java.Engine = (*Generator)(nil)

func (g *Generator) Name() string       { return "pg" }
func (g *Generator) Profile() string    { return "pg" }
func (g *Generator) DriverName() string { return "postgresql" }

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
	if spec.HasILIKE {
		sql = strings.ReplaceAll(sql, " LIKE ", " ILIKE ")
	}
	sql = strings.ReplaceAll(sql, " FROM dual", "")
	sql = strings.ReplaceAll(sql, "now()", "NOW()")
	return java.RenderMyBatisSQL(sql, spec.Params)
}

func (g *Generator) writeMethod(b *strings.Builder, spec engines.RunnerSpec, sql string, modelType string) {
	methodName := java.LowerFirst(spec.Query)
	annotation := stmtAnnotation(spec)
	// Use spec-level model type when available (but not for RETURNING scalar — those already have entity type)
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
		b.WriteString("    @Options(useGeneratedKeys = true, keyProperty = \"id\", keyColumn = \"id\")\n")
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
