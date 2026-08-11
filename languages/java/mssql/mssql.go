// Package mssql implements the Java/MyBatis MSSQL engine.
package mssql

import (
	"fmt"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/java"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.MSSQL} }

var _ java.Engine = (*Generator)(nil)

func (g *Generator) Name() string       { return "mssql" }
func (g *Generator) Profile() string    { return "mssql" }
func (g *Generator) DriverName() string { return "sqlserver" }

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
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok && ins.OnConflict != nil {
		return g.renderMerge(ins)
	}

	sql := g.d.Render(spec.Stmt)

	if spec.HasILIKE {
		sql = convertILIKEMSSQL(sql)
	}

	sql = strings.ReplaceAll(sql, "now()", "GETDATE()")
	sql = strings.ReplaceAll(sql, "NOW()", "GETDATE()")

	return java.RenderMyBatisSQL(sql, spec.Params)
}

func (g *Generator) writeMethod(b *strings.Builder, spec engines.RunnerSpec, sql string, modelType string) {
	methodName := java.LowerFirst(spec.Query)
	annotation := stmtAnnotation(spec)
	mt := modelType
	if spec.IsScalar && spec.Kind != engines.RunnerReturningScalar && spec.ModelType != "" {
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
		fmt.Fprintf(b, "    long %s(%s item);\n", methodName, modelType)
	}
}

func (g *Generator) renderMerge(ins *ast.InsertStmt) string {
	sql := g.d.Render(ins)
	oc := ins.OnConflict

	var colList, valList, onCond, setList strings.Builder
	for i, c := range ins.Columns {
		if i > 0 {
			colList.WriteString(", ")
			valList.WriteString(", ")
		}
		colList.WriteString(c)
		valList.WriteString("s." + c)
	}
	for i, c := range oc.Columns {
		if i > 0 {
			onCond.WriteString(" AND ")
		}
		onCond.WriteString("t." + c + " = s." + c)
	}
	if oc.DoUpdate {
		for i, s := range oc.Sets {
			if i > 0 {
				setList.WriteString(", ")
			}
			setList.WriteString(s.Col + " = s." + s.Col)
		}
	}

	valStart := strings.Index(sql, "VALUES ") + 7
	valsClause := strings.TrimSpace(sql[valStart:])

	if oc.DoUpdate {
		return fmt.Sprintf("MERGE INTO %s AS t USING (VALUES (%s)) AS s(%s) ON (%s) WHEN MATCHED THEN UPDATE SET %s WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s);",
			ins.Table.Name, valsClause, colList.String(), onCond.String(), setList.String(), colList.String(), valList.String())
	}
	return fmt.Sprintf("MERGE INTO %s AS t USING (VALUES (%s)) AS s(%s) ON (%s) WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s);",
		ins.Table.Name, valsClause, colList.String(), onCond.String(), colList.String(), valList.String())
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

func convertILIKEMSSQL(sql string) string {
	// Same as MySQL/Oracle
	result := strings.ReplaceAll(sql, " like ", " LIKE ")
	result = strings.ReplaceAll(result, " LIKE ", " LOWER LIKE ")
	return result
}

func escapeJava(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
