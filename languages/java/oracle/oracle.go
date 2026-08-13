// Package oracle implements the Java/MyBatis Oracle engine.
package oracle

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/java"
)

type Generator struct{ d *ast.Dialect }

func New() Generator { return Generator{d: ast.Ora} }

var _ java.Engine = (*Generator)(nil)

func (g *Generator) Name() string       { return "oracle" }
func (g *Generator) Profile() string    { return "oracle" }
func (g *Generator) DriverName() string { return "oracle" }

func (g *Generator) GenMapper(stem string, specs []engines.RunnerSpec, modelType string, th java.TypeHandlerFn) string {
	var b strings.Builder
	for _, spec := range specs {
		sql := g.renderSQL(spec, th)
		g.writeMethod(&b, spec, sql, modelType, th)
	}
	b.WriteString("\n")
	return b.String()
}

func (g *Generator) renderSQL(spec engines.RunnerSpec, th java.TypeHandlerFn) string {
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok && ins.OnConflict != nil {
		return g.renderMerge(ins, spec, th)
	}

	sql := g.d.Render(spec.Stmt)

	if spec.HasILIKE {
		sql = convertILIKEOracle(sql)
	}

	sql = strings.ReplaceAll(sql, "now()", "SYSDATE")
	sql = strings.ReplaceAll(sql, "NOW()", "SYSDATE")
	sql = replaceWord(sql, "TRUE", "1")
	sql = replaceWord(sql, "FALSE", "0")
	sql = replaceWord(sql, "true", "1")
	sql = replaceWord(sql, "false", "0")

	// Strip RETURNING — Oracle requires INTO clause which MyBatis doesn't support.
	if spec.Kind == engines.RunnerReturningScalar {
		sql = returningRe.ReplaceAllString(sql, "")
		// Inject id column with #{id} placeholder (filled by @SelectKey before=true)
		sql = injectOracleID(sql, java.InsertIDColumn(spec), java.InsertIDName(spec))
		return java.RenderMyBatisSQLWithNames(sql, spec.InsertParam, th)
	}

	return java.RenderMyBatisSQL(sql, spec.Params, th)
}

var (
	returningRe    = regexp.MustCompile(`\s+RETURNING\s+\S+\s*$`)
	insertValuesRe = regexp.MustCompile(`(INSERT INTO \w+) \(([^)]+)\) VALUES \(([^)]+)\)`)
)

// injectOracleID prepends the id column and #{id} placeholder for Oracle.
// "INSERT INTO t (col1) VALUES (:1)" → "INSERT INTO t (id, col1) VALUES (#{id}, :1)"
func injectOracleID(sql, idColumn, idName string) string {
	return insertValuesRe.ReplaceAllString(sql, "$1 ("+idColumn+", $2) VALUES (#{"+idName+"}, $3)")
}

func (g *Generator) writeMethod(b *strings.Builder, spec engines.RunnerSpec, sql string, modelType string, th java.TypeHandlerFn) {
	methodName := java.LowerFirst(spec.Query)
	annotation := stmtAnnotation(spec)
	mt := modelType
	if spec.ModelType != "" && spec.Kind != engines.RunnerReturningScalar {
		mt = spec.ModelType
	}

	switch spec.Kind {
	case engines.RunnerQueryOne, engines.RunnerQueryMany:
		fmt.Fprintf(b, "\n    @Override\n")
		if r := java.RenderResults(spec, th); r != "" {
			fmt.Fprintf(b, "    %s\n", r)
		}
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
		seqName := deriveSeqName(spec)
		fmt.Fprintf(b, "\n    @Override\n")
		fmt.Fprintf(b, "    @Insert(\"%s\")\n", escapeJava(sql))
		fmt.Fprintf(b, "    @SelectKey(statement = \"SELECT %s.NEXTVAL FROM dual\",\n", seqName)
		fmt.Fprintf(b, "               keyProperty = \"%s\", before = true, resultType = Long.class)\n", java.InsertIDName(spec))
		fmt.Fprintf(b, "    void %s(%s %s);\n", methodName, java.InsertParamType(spec), java.InsertParamArg(spec))
	}
}

func deriveSeqName(spec engines.RunnerSpec) string {
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok {
		return "seq_" + ins.Table.Name
	}
	return "seq_items"
}

func (g *Generator) renderMerge(ins *ast.InsertStmt, spec engines.RunnerSpec, th java.TypeHandlerFn) string {
	oc := ins.OnConflict
	sql := g.d.Render(ins)
	// Strip RETURNING — Oracle requires INTO clause which MyBatis doesn't support.
	sql = returningRe.ReplaceAllString(sql, "")
	if !oc.DoUpdate {
		// DO NOTHING: simple INSERT wrapped in PL/SQL block
		if spec.Kind == engines.RunnerReturningScalar {
			sql = injectOracleID(sql, java.InsertIDColumn(spec), java.InsertIDName(spec))
			sql = java.RenderMyBatisSQLWithNames(sql, spec.InsertParam, th)
		} else {
			sql = java.RenderMyBatisSQL(sql, spec.Params, th)
		}
		return "BEGIN " + sql + "; EXCEPTION WHEN DUP_VAL_ON_INDEX THEN NULL; END;"
	}
	return g.renderMergeUpdate(ins, spec)
}

func (g *Generator) renderMergeUpdate(ins *ast.InsertStmt, spec engines.RunnerSpec) string {
	oc := ins.OnConflict
	idColumn := java.InsertIDColumn(spec)
	idName := java.InsertIDName(spec)

	// Build USING subquery: SELECT #{p1} AS col1, #{p2} AS col2 FROM dual
	var usingCols strings.Builder
	for i := range ins.Columns {
		if i > 0 {
			usingCols.WriteString(", ")
		}
		fmt.Fprintf(&usingCols, "#{%s} AS %s", spec.InsertParam.MyBatisNames[i], ins.Columns[i])
	}

	// INSERT column/value lists: prepend id with #{id} (from @SelectKey)
	var colList, valList strings.Builder
	colList.WriteString(idColumn)
	valList.WriteString("#{" + idName + "}")
	for _, c := range ins.Columns {
		colList.WriteString(", ")
		valList.WriteString(", ")
		colList.WriteString(c)
		valList.WriteString("s." + c)
	}

	var onCond, setList strings.Builder
	for i, c := range oc.Columns {
		if i > 0 {
			onCond.WriteString(" AND ")
		}
		onCond.WriteString("t." + c + " = s." + c)
	}
	for i, s := range oc.Sets {
		if i > 0 {
			setList.WriteString(", ")
		}
		setList.WriteString(s.Col + " = s." + s.Col)
	}

	return fmt.Sprintf("MERGE INTO %s t USING (SELECT %s FROM dual) s ON (%s) WHEN MATCHED THEN UPDATE SET %s WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)",
		ins.Table.Name, usingCols.String(), onCond.String(), setList.String(), colList.String(), valList.String())
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

var ilikeOracleRe = regexp.MustCompile(`(\w+(?:\.\w+)?)\s+like\s+(\S+)`)

func convertILIKEOracle(sql string) string {
	result := ilikeOracleRe.ReplaceAllStringFunc(sql, func(match string) string {
		parts := ilikeOracleRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return fmt.Sprintf("LOWER(%s) LIKE LOWER(%s)", parts[1], parts[2])
	})
	result = strings.ReplaceAll(result, " like ", " LIKE ")
	return result
}

// replaceWord replaces whole-word matches (case-sensitive).
func replaceWord(s, old, new string) string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
	return re.ReplaceAllString(s, new)
}

func escapeJava(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
