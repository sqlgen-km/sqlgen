package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"vitess.io/vitess/go/vt/sqlparser"
)

// -------------------- SQL Preprocessing --------------------

// sqlPrep holds the result of SQL preprocessing.
type sqlPrep struct {
	cleanedSQL string     // SQL with @param replaced by :vN placeholders
	params     []paramRef // ordered param references
	returning  []string   // extracted RETURNING columns
	hasStar    bool       // RETURNING * was found
	hasILIKE   bool       // original DSL used ILIKE (replaced with LIKE for vitess)
	onConflict *onConflictInfo
}

type onConflictInfo struct {
	Columns   []string
	DoUpdate  bool
	Sets      []setClauseInfo
}

type setClauseInfo struct {
	Col string
	Val string
}

// paramRef records how a DSL @reference maps to function args.
type paramRef struct {
	Full    string // e.g. "filter.gender" or "id"
	Field   string // field name in AST: "gender" or "id"
	Param   string // function param name: "filter" or "id"
	IsField bool   // true if this is param.field access
}

// preprocessSQL extracts RETURNING and replaces @param references.
func preprocessSQL(rawSQL string) *sqlPrep {
	p := &sqlPrep{}
	sql := strings.TrimSpace(rawSQL)
	sql = p.extractReturning(sql)
	sql = p.extractOnConflict(sql)
	sql = p.replaceILIKE(sql)
	sql = p.replaceParams(sql)
	p.cleanedSQL = sql
	return p
}

var returningRe = regexp.MustCompile(`(?i)\s+RETURNING\s+(\*|(?:\w+\s*,\s*)*\w+)\s*$`)

func (p *sqlPrep) extractReturning(sql string) string {
	m := returningRe.FindStringSubmatch(sql)
	if m == nil {
		return sql
	}
	cols := m[1]
	if cols == "*" {
		p.hasStar = true
	} else {
		for _, part := range strings.Split(cols, ",") {
			p.returning = append(p.returning, strings.TrimSpace(part))
		}
	}
	return returningRe.ReplaceAllString(sql, "")
}

var onConflictRe = regexp.MustCompile(`(?si)\s+ON\s+CONFLICT\s+\(([^)]+)\)\s+DO\s+(NOTHING|UPDATE\s+SET\s+(.+?))(?:\s+RETURNING\s+.+)?$`)

func (p *sqlPrep) extractOnConflict(sql string) string {
	m := onConflictRe.FindStringSubmatch(sql)
	if m == nil {
		return sql
	}
	info := &onConflictInfo{}
	for _, c := range strings.Split(m[1], ",") {
		info.Columns = append(info.Columns, strings.TrimSpace(c))
	}
	if m[2] == "NOTHING" {
		info.DoUpdate = false
	} else {
		info.DoUpdate = true
		// parse SET clauses: "col = val, col2 = val2"
		setsStr := strings.TrimSpace(m[3])
		for _, set := range strings.Split(setsStr, ",") {
			parts := strings.SplitN(strings.TrimSpace(set), "=", 2)
			if len(parts) == 2 {
				info.Sets = append(info.Sets, setClauseInfo{
					Col: strings.TrimSpace(parts[0]),
					Val: strings.TrimSpace(parts[1]),
				})
			}
		}
	}
	p.onConflict = info
	return onConflictRe.ReplaceAllString(sql, "")
}

var paramRe = regexp.MustCompile(`@(\w+(?:\.\w+)?)`)

var ilikeRe = regexp.MustCompile(`(?i)\s+ILIKE\s+`)

// replaceILIKE replaces ILIKE with LIKE for vitess compatibility.
// The PG engine will restore ILIKE when rendering SQL.
func (p *sqlPrep) replaceILIKE(sql string) string {
	result := ilikeRe.ReplaceAllString(sql, " LIKE ")
	if result != sql {
		p.hasILIKE = true
	}
	return result
}
func (p *sqlPrep) replaceParams(sql string) string {
	var params []paramRef

	result := paramRe.ReplaceAllStringFunc(sql, func(match string) string {
		name := match[1:] // strip @

		ref := paramRef{Full: name}
		if dotIdx := strings.IndexByte(name, '.'); dotIdx >= 0 {
			ref.Param = name[:dotIdx]
			ref.Field = name[dotIdx+1:]
			ref.IsField = true
		} else {
			ref.Param = name
			ref.Field = name
		}
		params = append(params, ref)
		return "?"
	})

	p.params = params
	return result
}

// -------------------- Vitess AST Mapping --------------------

// parseSQLStmt parses SQL with vitess and maps to sqlgen AST.
func parseSQLStmt(sql string) (ast.Statement, error) {
	var parser sqlparser.Parser
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("sql parse: %w\nSQL: %s", err, sql)
	}

	switch s := stmt.(type) {
	case *sqlparser.Select:
		return mapSelect(s), nil
	case *sqlparser.Insert:
		return mapInsert(s), nil
	case *sqlparser.Update:
		return mapUpdate(s), nil
	case *sqlparser.Delete:
		return mapDelete(s), nil
	default:
		return nil, fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

// -------------------- SELECT --------------------

func mapSelect(s *sqlparser.Select) *ast.SelectStmt {
	out := &ast.SelectStmt{}

	// Columns
	if s.SelectExprs != nil {
		for _, se := range s.SelectExprs.Exprs {
			out.Columns = append(out.Columns, mapSelectExpr(se))
		}
	}

	// From — first entry is the main table, additional entries may be JOINs.
	// vitess may wrap FROM t1 JOIN t2 as a single JoinTableExpr in From[0].
	if len(s.From) > 0 {
		if jt, ok := s.From[0].(*sqlparser.JoinTableExpr); ok {
			// Unwrap the outer JoinTableExpr to extract main table + joins
			out.From = mapTableExpr(jt.LeftExpr)
			out.Joins = append(out.Joins, mapJoin(jt))
		} else {
			out.From = mapTableExpr(s.From[0])
		}

		for i := 1; i < len(s.From); i++ {
			if jt, ok := s.From[i].(*sqlparser.JoinTableExpr); ok {
				out.Joins = append(out.Joins, mapJoin(jt))
			} else {
				// Implicit cross join
				out.Joins = append(out.Joins, ast.JoinClause{
					Type:  ast.CrossJoin,
					Table: mapTableExpr(s.From[i]),
				})
			}
		}
	}

	// WHERE
	if s.Where != nil {
		e := mapExpr(s.Where.Expr)
		out.Where = &e
	}

	// GROUP BY
	if s.GroupBy != nil {
		for _, gb := range s.GroupBy.Exprs {
			out.GroupBy = append(out.GroupBy, mapExpr(gb))
		}
	}

	// HAVING
	if s.Having != nil {
		e := mapExpr(s.Having.Expr)
		out.Having = &e
	}

	// ORDER BY
	for _, ob := range s.OrderBy {
		out.OrderBy = append(out.OrderBy, ast.OrderClause{
			Expr: mapExpr(ob.Expr),
			Desc: ob.Direction == sqlparser.DescOrder,
		})
	}

	// LIMIT
	if s.Limit != nil {
		lc := &ast.LimitClause{
			Count: mapExpr(s.Limit.Rowcount),
		}
		if s.Limit.Offset != nil {
			lc.Offset = mapExpr(s.Limit.Offset)
		}
		out.Limit = lc
	}

	out.Distinct = s.Distinct
	return out
}

func mapSelectExpr(se sqlparser.SelectExpr) ast.Expr {
	switch e := se.(type) {
	case *sqlparser.StarExpr:
		return ast.Expr{Kind: ast.ExprStar}
	case *sqlparser.AliasedExpr:
		expr := mapExpr(e.Expr)
		if !e.As.IsEmpty() {
			expr.Alias = e.As.String()
		}
		return expr
	default:
		// Other SelectExpr implementations — use String format as fallback
		return ast.Expr{Kind: ast.ExprCol, Col: sqlparser.String(se)}
	}
}

func mapJoin(jt *sqlparser.JoinTableExpr) ast.JoinClause {
	jc := ast.JoinClause{
		Type:  mapJoinType(jt.Join),
		Table: mapTableExpr(jt.RightExpr),
	}
	if jt.Condition != nil {
		if jt.Condition.On != nil {
			e := mapExpr(jt.Condition.On)
			jc.On = &e
		}
		if jt.Condition.Using != nil {
			for _, c := range jt.Condition.Using {
				jc.Using = append(jc.Using, c.String())
			}
		}
	}
	return jc
}

func mapJoinType(jt sqlparser.JoinType) ast.JoinType {
	switch jt {
	case sqlparser.LeftJoinType, sqlparser.NaturalLeftJoinType:
		return ast.LeftJoin
	case sqlparser.RightJoinType, sqlparser.NaturalRightJoinType:
		return ast.RightJoin
	case sqlparser.NaturalJoinType:
		return ast.InnerJoin
	default:
		return ast.InnerJoin
	}
}

// -------------------- INSERT --------------------

func mapInsert(s *sqlparser.Insert) *ast.InsertStmt {
	out := &ast.InsertStmt{
		Table: mapTableName(s.Table.Expr),
	}

	if s.Columns != nil {
		for _, c := range s.Columns {
			out.Columns = append(out.Columns, c.String())
		}
	}

	switch rows := s.Rows.(type) {
	case sqlparser.Values:
		for _, row := range rows {
			var vals []ast.Expr
			for _, v := range row {
				vals = append(vals, mapExpr(v))
			}
			out.Values = append(out.Values, vals)
		}
	case *sqlparser.Select:
		out.Select = mapSelect(rows)
	}

	return out
}

// -------------------- UPDATE --------------------

func mapUpdate(s *sqlparser.Update) *ast.UpdateStmt {
	out := &ast.UpdateStmt{
		Table: mapTableExpr(s.TableExprs[0]),
	}

	for _, expr := range s.Exprs {
		out.Sets = append(out.Sets, ast.SetClause{
			Col: expr.Name.Name.String(),
			Val: mapExpr(expr.Expr),
		})
	}

	if s.Where != nil {
		e := mapExpr(s.Where.Expr)
		out.Where = &e
	}

	return out
}

// -------------------- DELETE --------------------

func mapDelete(s *sqlparser.Delete) *ast.DeleteStmt {
	out := &ast.DeleteStmt{
		Table: mapTableExpr(s.TableExprs[0]),
	}

	if s.Where != nil {
		e := mapExpr(s.Where.Expr)
		out.Where = &e
	}

	return out
}

// -------------------- Expressions --------------------

func mapExpr(expr sqlparser.Expr) ast.Expr {
	if expr == nil {
		return ast.Expr{}
	}

	switch e := expr.(type) {
	case *sqlparser.ColName:
		return mapColName(e)
	case *sqlparser.Literal:
		return mapLiteral(e)
	case *sqlparser.ComparisonExpr:
		return mapComparison(e)
	case *sqlparser.AndExpr:
		return mapAndOr(e.Left, e.Right, "AND")
	case *sqlparser.OrExpr:
		return mapAndOr(e.Left, e.Right, "OR")
	case *sqlparser.NotExpr:
		x := mapExpr(e.Expr)
		return ast.Expr{Kind: ast.ExprUnary, Op: "NOT", Left: &x}
	case *sqlparser.IsExpr:
		return mapIsExpr(e)
	case *sqlparser.FuncExpr:
		return mapFuncExpr(e)
	case *sqlparser.Subquery:
		if sel, ok := e.Select.(*sqlparser.Select); ok {
			sub := mapSelect(sel)
			return ast.Expr{Kind: ast.ExprSubquery, Stmt: sub}
		}
		return ast.Expr{Kind: ast.ExprCol, Col: sqlparser.String(e)}
	case *sqlparser.ExistsExpr:
		sel, ok := e.Subquery.Select.(*sqlparser.Select)
		if !ok {
			return ast.Expr{Kind: ast.ExprCol, Col: sqlparser.String(e)}
		}
		sub := mapSelect(sel)
		return ast.Expr{Kind: ast.ExprExists, Stmt: sub}
	case *sqlparser.BinaryExpr:
		return mapBinaryExpr(e)
	case *sqlparser.UnaryExpr:
		x := mapExpr(e.Expr)
		return ast.Expr{Kind: ast.ExprUnary, Op: e.Operator.ToString(), Left: &x}
	case *sqlparser.NullVal:
		return ast.Expr{Kind: ast.ExprLiteral, Val: nil}
	case *sqlparser.Argument:
		// ? placeholder → named parameter, index tracked by prep.params order
		return ast.Expr{Kind: ast.ExprParam, Param: "_"}
	case sqlparser.ListArg:
		return ast.Expr{Kind: ast.ExprParam, Param: "_"}
	case *sqlparser.BetweenExpr:
		left := mapExpr(e.Left)
		from := mapExpr(e.From)
		to := mapExpr(e.To)
		if e.IsBetween {
			return ast.Expr{Kind: ast.ExprBetween, Left: &left, Low: &from, High: &to}
		}
		// NOT BETWEEN — treat as NOT (x BETWEEN ...)
		bet := ast.Expr{Kind: ast.ExprBetween, Left: &left, Low: &from, High: &to}
		return ast.Expr{Kind: ast.ExprUnary, Op: "NOT", Left: &bet}
	default:
		return ast.Expr{Kind: ast.ExprCol, Col: sqlparser.String(expr)}
	}
}

func mapColName(c *sqlparser.ColName) ast.Expr {
	col := c.Name.String()
	if !c.Qualifier.IsEmpty() {
		col = c.Qualifier.Name.String() + "." + col
	}
	return ast.Expr{Kind: ast.ExprCol, Col: col}
}

func mapLiteral(l *sqlparser.Literal) ast.Expr {
	val := l.Val
	if l.Type == sqlparser.StrVal {
		val = "'" + strings.ReplaceAll(l.Val, "'", "''") + "'"
	}
	return ast.Expr{Kind: ast.ExprLiteral, Val: val}
}

func mapAndOr(left, right sqlparser.Expr, op string) ast.Expr {
	l := mapExpr(left)
	r := mapExpr(right)
	return ast.Expr{Kind: ast.ExprBinary, Op: op, Left: &l, Right: &r}
}

func mapComparison(c *sqlparser.ComparisonExpr) ast.Expr {
	left := mapExpr(c.Left)

	// Handle IN / NOT IN
	if c.Operator == sqlparser.InOp || c.Operator == sqlparser.NotInOp {
		switch right := c.Right.(type) {
		case *sqlparser.Subquery:
			if sel, ok := right.Select.(*sqlparser.Select); ok {
				sub := mapSelect(sel)
				e := ast.Expr{Kind: ast.ExprIn, Left: &left, Stmt: sub}
				if c.Operator == sqlparser.NotInOp {
					return ast.Expr{Kind: ast.ExprUnary, Op: "NOT", Left: &e}
				}
				return e
			}
		case sqlparser.ValTuple:
			var items []ast.Expr
			for _, v := range right {
				items = append(items, mapExpr(v))
			}
			e := ast.Expr{Kind: ast.ExprIn, Left: &left, Items: items}
			if c.Operator == sqlparser.NotInOp {
				return ast.Expr{Kind: ast.ExprUnary, Op: "NOT", Left: &e}
			}
			return e
		default:
			// Single value IN
			rightExpr := mapExpr(c.Right)
			e := ast.Expr{Kind: ast.ExprIn, Left: &left, Items: []ast.Expr{rightExpr}}
			if c.Operator == sqlparser.NotInOp {
				return ast.Expr{Kind: ast.ExprUnary, Op: "NOT", Left: &e}
			}
			return e
		}
	}

	right := mapExpr(c.Right)
	op := c.Operator.ToString()
	return ast.Expr{Kind: ast.ExprBinary, Op: op, Left: &left, Right: &right}
}

func mapIsExpr(e *sqlparser.IsExpr) ast.Expr {
	left := mapExpr(e.Left)
	switch e.Right {
	case sqlparser.IsNullOp:
		return ast.Expr{Kind: ast.ExprIsNull, Left: &left}
	case sqlparser.IsNotNullOp:
		return ast.Expr{Kind: ast.ExprNotNull, Left: &left}
	default:
		// Other IS expressions (IS TRUE, IS FALSE) — render as binary
		right := ast.Expr{Kind: ast.ExprLiteral, Val: e.Right.ToString()}
		return ast.Expr{Kind: ast.ExprBinary, Op: "IS", Left: &left, Right: &right}
	}
}

func mapFuncExpr(e *sqlparser.FuncExpr) ast.Expr {
	name := e.Name.String()
	if !e.Qualifier.IsEmpty() {
		name = e.Qualifier.String() + "." + name
	}
	var args []ast.Expr
	for _, a := range e.Exprs {
		args = append(args, mapExpr(a))
	}
	return ast.Expr{Kind: ast.ExprCall, Name: name, Args: args}
}

func mapBinaryExpr(e *sqlparser.BinaryExpr) ast.Expr {
	left := mapExpr(e.Left)
	right := mapExpr(e.Right)
	op := e.Operator.ToString()
	return ast.Expr{Kind: ast.ExprBinary, Op: op, Left: &left, Right: &right}
}

// -------------------- Table / Join --------------------

func mapTableExpr(te sqlparser.TableExpr) ast.TableRef {
	switch t := te.(type) {
	case *sqlparser.AliasedTableExpr:
		ref := mapTableName(t.Expr)
		if !t.As.IsEmpty() {
			ref.Alias = t.As.String()
		}
		return ref
	case *sqlparser.JoinTableExpr:
		// Normally JOINs are mapped separately, but as fallback
		return mapTableName(t.LeftExpr.(*sqlparser.AliasedTableExpr).Expr)
	default:
		return ast.TableRef{Name: sqlparser.String(te)}
	}
}

func mapTableName(te sqlparser.SimpleTableExpr) ast.TableRef {
	switch t := te.(type) {
	case sqlparser.TableName:
		name := t.Name.String()
		if !t.Qualifier.IsEmpty() {
			name = t.Qualifier.String() + "." + name
		}
		return ast.TableRef{Name: name}
	default:
		return ast.TableRef{Name: sqlparser.String(te)}
	}
}
