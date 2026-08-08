// Package engines sqlgen 引擎接口

package engines

import (
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
)

// ── 共享类型（生成到 models.go / <name>.go）──

// Query 解析后的查询定义
type Query struct {
	Name      string           // Go 方法名
	Mode      string           // "one", "many", "exec", "execrows"
	Params    []Param          // 入参
	Return    string           // 返回类型名
	IsScalar  bool             // model int64 简写
	FieldMaps []FieldMap       // 返回字段映射
	Doc       []string         // 文档注释
	Stmt      ast.Statement // AST 语句
}

type Param struct {
	Name string
	Type string
}

type FieldMap struct {
	Field  string
	Column string
}

type Model struct {
	Name      string
	Fields    []Field
	FieldMaps []FieldMap
}

type Field struct {
	Name string
	Type string
}

// RunnerKind 操作运行器类型
type RunnerKind int

const (
	RunnerQueryOne        RunnerKind = iota // SELECT :one
	RunnerQueryMany                        // SELECT :many
	RunnerExec                             // :exec
	RunnerExecRows                         // :execrows
	RunnerReturningScalar                  // RETURNING id（单列标量）
	RunnerReturning                        // RETURNING a,b / RETURNING *（多列）
)

// RunnerParam describes a single parameter passed to a runner method.
type RunnerParam struct {
	Name string // Go parameter name (unique per runner method)
	Type string // Go type (e.g. "int64", "*string")
}

// RunnerSpec 单个操作的运行器定义（生成到共享代码）
type RunnerSpec struct {
	Name     string           // 运行器字段名（小写导出）
	Kind     RunnerKind       // 运行器类型
	Query    string           // 对应方法名
	IsScalar bool
	HasILIKE bool             // DSL used ILIKE
	Params   []RunnerParam    // 方法签名参数（类型化）
	Stmt     ast.Statement // AST 语句（引擎据此渲染 SQL 和决定执行策略）
}

// Engine 引擎接口
type Engine interface {
	Name() string
	// GenFile generates the complete engine file content (factory + all runners + SQL constants).
	GenFile(stem string, specs []RunnerSpec) string
}

// ParamSignature returns the Go method parameter signature for a runner's typed params,
// e.g. ", name *string, limit int32".
func (s RunnerSpec) ParamSignature() string {
	var b strings.Builder
	for _, p := range s.Params {
		b.WriteString(", ")
		b.WriteString(p.Name)
		b.WriteString(" ")
		b.WriteString(p.Type)
	}
	return b.String()
}

// QuoteIdent converts double-quoted identifiers to dialect-specific quoting.
// PG and Oracle keep ", MySQL converts to backticks, MSSQL to brackets.
func QuoteIdent(sql, dialect string) string {
	switch dialect {
	case "mysql":
		re := regexp.MustCompile(`"([^"]*)"`)
		return re.ReplaceAllString(sql, "`$1`")
	case "mssql":
		re := regexp.MustCompile(`"([^"]*)"`)
		return re.ReplaceAllString(sql, "[$1]")
	default:
		return sql
	}
}

// GoString returns a Go literal for the string — raw string if no backticks,
// otherwise an escaped regular string.
func GoString(s string) string {
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	// Escape for regular Go string
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return "\"" + s + "\""
}
func (s RunnerSpec) ParamNames() string {
	var b strings.Builder
	for _, p := range s.Params {
		b.WriteString(", ")
		b.WriteString(p.Name)
	}
	return b.String()
}
