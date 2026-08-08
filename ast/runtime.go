package ast

import (
	"database/sql"
)

// -------------------- Dialect --------------------

// Dialect 将 AST 翻译为方言 SQL。
type Dialect struct {
	name string
	ph   func(n int) string
}

var (
	pgDialect    = Dialect{name: "postgres", ph: func(n int) string { return "$" + itoa(n) }}
	oraDialect   = Dialect{name: "oracle", ph: func(n int) string { return ":" + itoa(n) }}
	mssqlDialect = Dialect{name: "sqlserver", ph: func(n int) string { return "@p" + itoa(n) }}
	mysqlDialect = Dialect{name: "mysql", ph: func(n int) string { return "?" }}
)

// PG 是 PostgreSQL 方言，占位符 $1, $2, ...
var PG = &pgDialect

// Ora 是 Oracle 方言，占位符 :1, :2, ...
var Ora = &oraDialect

// MSSQL 是 SQL Server 方言，占位符 @p1, @p2, ...
var MSSQL = &mssqlDialect

// MySQL 是 MySQL 方言，占位符 ?
var MySQL = &mysqlDialect

// Name 返回方言名。
func (d *Dialect) Name() string { return d.name }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [11]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// -------------------- 基础结构 --------------------

type stmtImpl struct {
	db   *sql.DB
	tx   *sql.Tx
	stmt *sql.Stmt
}

// PT 是执行器的公共方法：WithTx 绑定事务、Close 释放预编译语句。
type PT[T any] interface {
	WithTx(tx *sql.Tx) T
	Close() error
}
