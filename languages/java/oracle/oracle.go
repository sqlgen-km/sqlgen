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
		return g.renderMerge(ins, spec)
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
	}

	sql = java.RenderMyBatisSQL(sql, spec.Params)

	// Inject id column with seq.NEXTVAL: before=false @SelectKey uses CURRVAL
	// to return the generated ID.
	if spec.Kind == engines.RunnerReturningScalar {
		seqName := deriveSeqName(spec)
		sql = injectOracleID(sql, seqName)
	}

	return sql
}

var (
	returningRe    = regexp.MustCompile(`\s+RETURNING\s+\S+\s*$`)
	insertValuesRe = regexp.MustCompile(`(INSERT INTO \w+) \(([^)]+)\) VALUES \(([^)]+)\)`)
)

// injectOracleID prepends id to INSERT columns and injects seq.NEXTVAL for Oracle.
// "INSERT INTO t (col1) VALUES (#{p1})" → "INSERT INTO t (id, col1) VALUES (seq_t.NEXTVAL, #{p1})"
func injectOracleID(sql, seqName string) string {
	seqRef := seqName + ".NEXTVAL"
	return insertValuesRe.ReplaceAllString(sql, "$1 (id, $2) VALUES ("+seqRef+", $3)")
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
		seqName := deriveSeqName(spec)
		fmt.Fprintf(b, "\n    @Override\n")
		fmt.Fprintf(b, "    @Insert(\"%s\")\n", escapeJava(sql))
		fmt.Fprintf(b, "    @SelectKey(statement = \"SELECT %s.CURRVAL FROM dual\",\n", seqName)
		b.WriteString("               keyProperty = \"id\", before = false, resultType = long.class)\n")
		fmt.Fprintf(b, "    long %s(", methodName)
		writeParams(b, spec)
		b.WriteString(");\n")
	}
}

func deriveSeqName(spec engines.RunnerSpec) string {
	if ins, ok := spec.Stmt.(*ast.InsertStmt); ok {
		return "seq_" + ins.Table.Name
	}
	return "seq_items"
}

func (g *Generator) renderMerge(ins *ast.InsertStmt, spec engines.RunnerSpec) string {
	oc := ins.OnConflict
	sql := g.d.Render(ins)
	// Strip RETURNING — Oracle requires INTO clause which MyBatis doesn't support.
	sql = returningRe.ReplaceAllString(sql, "")
	sql = java.RenderMyBatisSQL(sql, spec.Params)
	if !oc.DoUpdate {
		// DO NOTHING: simple INSERT with seq.NEXTVAL + CURRVAL @SelectKey
		if spec.Kind == engines.RunnerReturningScalar {
			seqName := deriveSeqName(spec)
			sql = injectOracleID(sql, seqName)
		}
		return "BEGIN " + sql + "; EXCEPTION WHEN DUP_VAL_ON_INDEX THEN NULL; END;"
	}
	return g.renderMergeUpdate(ins, spec)
}

func (g *Generator) renderMergeUpdate(ins *ast.InsertStmt, spec engines.RunnerSpec) string {
	oc := ins.OnConflict
	seqName := "seq_" + ins.Table.Name

	// Build USING subquery: SELECT #{p1} AS col1, #{p2} AS col2 FROM dual
	var usingCols strings.Builder
	for i, p := range spec.Params {
		if i > 0 {
			usingCols.WriteString(", ")
		}
		fmt.Fprintf(&usingCols, "#{%s} AS %s", p.Name, ins.Columns[i])
	}

	// INSERT column/value lists: prepend id with seq.NEXTVAL
	var colList, valList strings.Builder
	colList.WriteString("id")
	valList.WriteString(seqName + ".NEXTVAL")
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
