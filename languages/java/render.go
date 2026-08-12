package java

import (
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/engines"
)

// Go2JavaType maps Go/DSL types to Java types.
func Go2JavaType(goType string) string {
	return go2javaType(goType)
}

// go2javaType maps Go/DSL types to Java types.
func go2javaType(goType string) string {
	switch goType {
	case "int64", "int":
		return "long"
	case "int32":
		return "int"
	case "float64":
		return "java.math.BigDecimal"
	case "string":
		return "String"
	case "bool":
		return "boolean"
	case "time.Time", "*time.Time":
		return "java.time.LocalDateTime"
	case "[]byte":
		return "byte[]"
	case "*string":
		return "String"
	case "*int32":
		return "Integer"
	case "*int64":
		return "Long"
	case "*bool":
		return "Boolean"
	case "*float64":
		return "java.math.BigDecimal"
	default:
		// Handle Go slice types: []string → String[], []int64 → long[], etc.
		if strings.HasPrefix(goType, "[]") {
			return go2javaType(goType[2:]) + "[]"
		}
		return goType
	}
}

// boxJavaType returns the boxed version for use in generic type arguments.
func boxJavaType(goType string) string {
	switch goType {
	case "int64", "int":
		return "Long"
	case "int32":
		return "Integer"
	case "float64":
		return "java.math.BigDecimal"
	case "string":
		return "String"
	case "bool":
		return "Boolean"
	default:
		return go2javaType(goType)
	}
}

// RenderMyBatisSQL converts a dialect SQL string (with positional placeholders like
// $1, ?, :1, @p1) to MyBatis #{paramName} placeholder syntax. The RunnerSpec.Params
// list provides the parameter names in order.
func RenderMyBatisSQL(dialectSQL string, params []engines.RunnerParam) string {
	sql := dialectSQL
	for _, p := range params {
		replacement := "#{" + p.Name + "}"
		sql = replaceFirstPlaceholder(sql, replacement)
	}
	return sql
}

var (
	reDollar = regexp.MustCompile(`\$\d+`)
	reColon  = regexp.MustCompile(`:\d+`)
	reAtP    = regexp.MustCompile(`@p\d+`)
)

// replaceFirstPlaceholder replaces the first positional placeholder found
// ($N, :N, @pN, or ?) with the given replacement string.
func replaceFirstPlaceholder(sql, replacement string) string {
	if loc := reDollar.FindStringIndex(sql); loc != nil {
		return sql[:loc[0]] + replacement + sql[loc[1]:]
	}
	if loc := reColon.FindStringIndex(sql); loc != nil {
		return sql[:loc[0]] + replacement + sql[loc[1]:]
	}
	if loc := reAtP.FindStringIndex(sql); loc != nil {
		return sql[:loc[0]] + replacement + sql[loc[1]:]
	}
	idx := strings.Index(sql, "?")
	if idx >= 0 {
		return sql[:idx] + replacement + sql[idx+1:]
	}
	return sql
}

// goTypeNeedsImport returns true if the Go type requires a java.time import.
func goTypeNeedsImport(goType string) bool {
	return strings.Contains(goType, "time.Time")
}

// goTypeNeedsMath returns true if the Go type requires a java.math import.
func goTypeNeedsMath(goType string) bool {
	return goType == "float64" || goType == "*float64"
}

// MethodReturnType returns the Java return type for a RunnerSpec.
func MethodReturnType(spec engines.RunnerSpec, modelType string) string {
	switch spec.Kind {
	case engines.RunnerQueryOne:
		if spec.IsScalar {
			return go2javaType(modelType)
		}
		return modelType
	case engines.RunnerQueryMany:
		if spec.IsScalar {
			return "List<" + boxJavaType(modelType) + ">"
		}
		return "List<" + modelType + ">"
	case engines.RunnerExec:
		return "void"
	case engines.RunnerExecRows:
		return "long"
	case engines.RunnerReturningScalar:
		return "long"
	default:
		return "void"
	}
}

// MethodAnnotation returns the MyBatis annotation and annotation name for a RunnerSpec.
// Returns annotation name and a boolean indicating if it's an @Insert/@Update/@Delete (exec type).
func MethodAnnotation(spec engines.RunnerSpec) (string, bool) {
	switch spec.Kind {
	case engines.RunnerQueryOne, engines.RunnerQueryMany:
		return "@Select", false
	case engines.RunnerExec, engines.RunnerExecRows:
		return "@Insert", true // engine overrides for UPDATE/DELETE
	case engines.RunnerReturningScalar:
		return "@Insert", true
	default:
		return "", false
	}
}
