package meta

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlgen-km/sqlgen/ast"
	"vitess.io/vitess/go/vt/sqlparser"
)

// ParseSQLStmt parses SQL with vitess and maps to sqlgen AST.
func ParseSQLStmt(sql string) (ast.Statement, error) {
	// vitess MySQL parser treats "identifier" as string literal.
	// Convert to backtick-quoted identifiers that vitess accepts.
	sql = convertDoubleQuotes(sql)

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
			col := unquoteMarker(c.String())
			out.Columns = append(out.Columns, col)
		}
	}

	// Also mark the table as quoted if needed
	if !out.Table.Quoted {
		if qt, ok := s.Table.Expr.(sqlparser.TableName); ok {
			name := qt.Name.String()
			if strings.HasPrefix(name, "qqq") && strings.HasSuffix(name, "qqq") && len(name) > 6 {
				out.Table.Quoted = true
				out.Table.Name = name[3 : len(name)-3]
			}
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
		// ? placeholder → named parameter, index tracked by prep.Params order
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
	quoted := false
	if strings.HasPrefix(col, "qqq") && strings.HasSuffix(col, "qqq") && len(col) > 6 {
		col = col[3:]
		col = col[:len(col)-3]
		quoted = true
	}
	if !c.Qualifier.IsEmpty() {
		col = c.Qualifier.Name.String() + "." + col
		quoted = false // qualified names are never quoted
	}
	return ast.Expr{Kind: ast.ExprCol, Col: col, Quoted: quoted}
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

	// Array membership marker: `= __sqlgen_any__(@arr)` (rewritten from `= ANY(@arr)`).
	if fe, ok := c.Right.(*sqlparser.FuncExpr); ok && fe.Name.String() == "__sqlgen_any__" {
		var arg ast.Expr
		if len(fe.Exprs) > 0 {
			arg = mapExpr(fe.Exprs[0])
		} else {
			arg = ast.Expr{Kind: ast.ExprParam, Param: "_"}
		}
		return ast.Expr{Kind: ast.ExprAny, Left: &left, Right: &arg}
	}

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
		quoted := false
		if strings.HasPrefix(name, "__sqlgen_q_") && strings.HasSuffix(name, "__") {
			name = name[len("__sqlgen_q_"):]
			name = name[:len(name)-2]
			quoted = true
		}
		if !t.Qualifier.IsEmpty() {
			name = t.Qualifier.String() + "." + name
			quoted = false
		}
		return ast.TableRef{Name: name, Quoted: quoted}
	default:
		return ast.TableRef{Name: sqlparser.String(te)}
	}
}

// unquoteMarker strips the qqq...qqq marker and returns a double-quoted name.
func unquoteMarker(s string) string {
	if strings.HasPrefix(s, "qqq") && strings.HasSuffix(s, "qqq") && len(s) > 6 {
		return `"` + s[3:len(s)-3] + `"`
	}
	return s
}

var dqRe = regexp.MustCompile(`"([^"]*)"`)

func convertDoubleQuotes(sql string) string {
	return dqRe.ReplaceAllString(sql, "qqq${1}qqq")
}
