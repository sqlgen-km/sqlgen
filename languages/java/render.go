package java

import (
	"fmt"
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
	case "*int":
		return "Long"
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
	case "int64", "int", "*int":
		return "Long"
	case "int32", "*int32":
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

// TypeHandlerFn resolves the fully-qualified TypeHandler class name for a Go
// slice type, or "" if the type is not an array (or no TypeHandler is needed).
type TypeHandlerFn func(goType string) string

// RenderMyBatisSQL converts a dialect SQL string (with positional placeholders like
// $1, ?, :1, @p1) to MyBatis #{paramName} placeholder syntax. The RunnerSpec.Params
// list provides the parameter names in order. th, when non-nil, appends a
// `typeHandler=` attribute for array params.
func RenderMyBatisSQL(dialectSQL string, params []engines.RunnerParam, th TypeHandlerFn) string {
	sql := dialectSQL
	for _, p := range params {
		replacement := "#{" + p.Name
		if th != nil {
			if fqn := th(p.Type); fqn != "" {
				replacement += ", typeHandler=" + fqn
			}
		}
		replacement += "}"
		sql = replaceFirstPlaceholder(sql, replacement)
	}
	return sql
}

// RenderMyBatisSQLWithNames is like RenderMyBatisSQL but takes the INSERT RETURNING
// parameter object, whose fields (and their Go types) provide the placeholder
// names (camelCase) aligned with the SQL parameter order.
func RenderMyBatisSQLWithNames(dialectSQL string, ip *engines.InsertParam, th TypeHandlerFn) string {
	sql := dialectSQL
	for i, n := range ip.MyBatisNames {
		replacement := "#{" + n
		if th != nil && i < len(ip.Fields) {
			if fqn := th(ip.Fields[i].GoType); fqn != "" {
				replacement += ", typeHandler=" + fqn
			}
		}
		replacement += "}"
		sql = replaceFirstPlaceholder(sql, replacement)
	}
	return sql
}

// InsertParamType returns the parameter object type name for an INSERT RETURNING
// method: the reused model name (single-object scenario) or "{Query}Params".
func InsertParamType(spec engines.RunnerSpec) string {
	if spec.InsertParam != nil && spec.InsertParam.ReuseModel != "" {
		return spec.InsertParam.ReuseModel
	}
	return spec.Query + "Params"
}

// InsertParamArg returns the parameter variable name for an INSERT RETURNING
// method: lowerCamel of the model name, or "params" for a generated class.
func InsertParamArg(spec engines.RunnerSpec) string {
	if spec.InsertParam != nil && spec.InsertParam.ReuseModel != "" {
		return LowerFirst(spec.InsertParam.ReuseModel)
	}
	return "params"
}

// InsertIDName returns the camelCase Java field name for the generated key.
func InsertIDName(spec engines.RunnerSpec) string {
	if spec.InsertParam != nil && spec.InsertParam.IDName != "" {
		return spec.InsertParam.IDName
	}
	return "id"
}

// InsertIDColumn returns the DB column name for the generated key.
func InsertIDColumn(spec engines.RunnerSpec) string {
	if spec.InsertParam != nil && spec.InsertParam.IDColumn != "" {
		return spec.InsertParam.IDColumn
	}
	return "id"
}

var (
	reDollar = regexp.MustCompile(`\$\d+`)
	reColon  = regexp.MustCompile(`:\d+`)
	reAtP    = regexp.MustCompile(`@p\d+`)
)

// RenderResults generates the @Results annotation (with typeHandler) for array
// result columns of a SELECT, or "" if none.
func RenderResults(spec engines.RunnerSpec, th TypeHandlerFn) string {
	if th == nil || len(spec.ArrayColumns) == 0 {
		return ""
	}
	var entries []string
	for _, c := range spec.ArrayColumns {
		fqn := th(c.GoType)
		if fqn == "" {
			continue
		}
		entries = append(entries, fmt.Sprintf("@Result(column=\"%s\", property=\"%s\", typeHandler=%s.class)", c.Column, LowerFirst(c.Field), fqn))
	}
	if len(entries) == 0 {
		return ""
	}
	return "@Results({\n        " + strings.Join(entries, ",\n        ") + "\n    })"
}

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
	return strings.Contains(goType, "float64")
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
		// Key is injected into the parameter object; method returns void.
		return "void"
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
