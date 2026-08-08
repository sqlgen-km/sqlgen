package ast

import "strings"

// -------------------- Renderer --------------------

// renderer walks the AST and produces dialect-specific SQL.
type renderer struct {
	d      *Dialect
	b      strings.Builder
	paramN int
}

func (r *renderer) nextParam() string {
	r.paramN++
	return r.d.ph(r.paramN)
}

// Render produces dialect SQL from a Statement AST.
func (d *Dialect) Render(stmt Statement) string {
	r := &renderer{d: d}
	r.renderStmt(stmt)
	return r.b.String()
}

func (r *renderer) renderStmt(stmt Statement) {
	switch s := stmt.(type) {
	case *SelectStmt:
		r.renderSelect(s)
	case *InsertStmt:
		r.renderInsert(s)
	case *UpdateStmt:
		r.renderUpdate(s)
	case *DeleteStmt:
		r.renderDelete(s)
	}
}

// -------------------- SELECT --------------------

func (r *renderer) renderSelect(s *SelectStmt) {
	r.b.WriteString("SELECT ")
	if s.Distinct {
		r.b.WriteString("DISTINCT ")
	}
	r.renderExprs(s.Columns)
	r.b.WriteString(" FROM ")
	r.renderTable(s.From)
	for _, j := range s.Joins {
		r.b.WriteByte(' ')
		r.renderJoin(j)
	}
	if s.Where != nil {
		r.b.WriteString(" WHERE ")
		r.renderExpr(*s.Where)
	}
	if len(s.GroupBy) > 0 {
		r.b.WriteString(" GROUP BY ")
		r.renderExprs(s.GroupBy)
	}
	if s.Having != nil {
		r.b.WriteString(" HAVING ")
		r.renderExpr(*s.Having)
	}
	if len(s.OrderBy) > 0 {
		r.b.WriteString(" ORDER BY ")
		r.renderOrders(s.OrderBy)
	}
	if s.Limit != nil {
		r.renderLimit(s.Limit)
	}
	if s.ForUpdate {
		r.b.WriteString(" FOR UPDATE")
	}
}

// -------------------- INSERT --------------------

func (r *renderer) renderInsert(s *InsertStmt) {
	r.b.WriteString("INSERT INTO ")
	r.renderTable(s.Table)
	if len(s.Columns) > 0 {
		r.b.WriteString(" (")
		r.b.WriteString(strings.Join(s.Columns, ", "))
		r.b.WriteByte(')')
	}
	if s.Select != nil {
		r.b.WriteByte(' ')
		r.renderSelect(s.Select)
	} else {
		r.b.WriteString(" VALUES ")
		for i, row := range s.Values {
			if i > 0 {
				r.b.WriteString(", ")
			}
			r.b.WriteByte('(')
			r.renderExprs(row)
			r.b.WriteByte(')')
		}
	}
	if len(s.Returning) > 0 {
		r.renderReturning(s.Returning)
	}
}

// -------------------- UPDATE --------------------

func (r *renderer) renderUpdate(s *UpdateStmt) {
	r.b.WriteString("UPDATE ")
	r.renderTable(s.Table)
	r.b.WriteString(" SET ")
	for i, set := range s.Sets {
		if i > 0 {
			r.b.WriteString(", ")
		}
		r.b.WriteString(set.Col)
		r.b.WriteString(" = ")
		r.renderExpr(set.Val)
	}
	if len(s.From) > 0 {
		r.b.WriteString(" FROM ")
		for i, t := range s.From {
			if i > 0 {
				r.b.WriteString(", ")
			}
			r.renderTable(t)
		}
	}
	if s.Where != nil {
		r.b.WriteString(" WHERE ")
		r.renderExpr(*s.Where)
	}
}

// -------------------- DELETE --------------------

func (r *renderer) renderDelete(s *DeleteStmt) {
	r.b.WriteString("DELETE FROM ")
	r.renderTable(s.Table)
	if len(s.Using) > 0 {
		r.b.WriteString(" USING ")
		for i, t := range s.Using {
			if i > 0 {
				r.b.WriteString(", ")
			}
			r.renderTable(t)
		}
	}
	if s.Where != nil {
		r.b.WriteString(" WHERE ")
		r.renderExpr(*s.Where)
	}
}

// -------------------- Clauses --------------------

func (r *renderer) renderTable(t TableRef) {
	if t.Quoted {
		r.b.WriteByte('"')
		r.b.WriteString(t.Name)
		r.b.WriteByte('"')
	} else {
		r.b.WriteString(t.Name)
	}
	if t.Alias != "" {
		r.b.WriteString(" ")
		r.b.WriteString(t.Alias)
	}
}

func (r *renderer) renderJoin(j JoinClause) {
	r.b.WriteString(j.Type.String())
	r.b.WriteByte(' ')
	r.renderTable(j.Table)
	if len(j.Using) > 0 {
		r.b.WriteString(" USING (")
		r.b.WriteString(strings.Join(j.Using, ", "))
		r.b.WriteByte(')')
	} else if j.On != nil {
		r.b.WriteString(" ON ")
		r.renderExpr(*j.On)
	}
}

func (r *renderer) renderOrders(orders []OrderClause) {
	for i, o := range orders {
		if i > 0 {
			r.b.WriteString(", ")
		}
		r.renderExpr(o.Expr)
		if o.Desc {
			r.b.WriteString(" DESC")
		}
		if o.NullsFirst {
			r.b.WriteString(" NULLS FIRST")
		} else if o.NullsLast {
			r.b.WriteString(" NULLS LAST")
		}
	}
}

func (r *renderer) renderLimit(l *LimitClause) {
	isOracle := r.d.name == "oracle"

	if isOracle {
		// Oracle 12c+: OFFSET x ROWS FETCH NEXT y ROWS ONLY
		// Note: param order in DSL is (limit, offset), but Oracle syntax
		// renders offset first then limit, so args need swapping.
		// The Oracle engine handles arg swapping.
		if l.hasOffset() {
			r.b.WriteString(" OFFSET ")
			r.renderExpr(l.Offset)
			r.b.WriteString(" ROWS")
		}
		r.b.WriteString(" FETCH NEXT ")
		r.renderExpr(l.Count)
		r.b.WriteString(" ROWS ONLY")
	} else {
		// PG, MySQL
		r.b.WriteString(" LIMIT ")
		r.renderExpr(l.Count)
		if l.hasOffset() {
			r.b.WriteString(" OFFSET ")
			r.renderExpr(l.Offset)
		}
	}
}

func (r *renderer) renderReturning(cols []string) {
	r.b.WriteString(" RETURNING ")
	r.b.WriteString(strings.Join(cols, ", "))
}

// -------------------- Expressions --------------------

func (r *renderer) renderExprs(exprs []Expr) {
	for i, e := range exprs {
		if i > 0 {
			r.b.WriteString(", ")
		}
		r.renderExpr(e)
	}
}

func (r *renderer) renderExpr(e Expr) {
	switch e.Kind {
	case ExprCol:
		if e.Quoted {
			r.b.WriteByte('"')
			r.b.WriteString(e.Col)
			r.b.WriteByte('"')
		} else {
			r.b.WriteString(e.Col)
		}
	case ExprLiteral:
		if e.Val == nil {
			r.b.WriteString("NULL")
		} else {
			r.b.WriteString(e.Val.(string))
		}
	case ExprParam:
		r.b.WriteString(r.nextParam())
	case ExprBinary:
		r.renderBinary(e)
	case ExprUnary:
		r.b.WriteString(e.Op)
		r.b.WriteByte(' ')
		r.renderExpr(*e.Left)
	case ExprCall:
		r.b.WriteString(e.Name)
		r.b.WriteByte('(')
		r.renderExprs(e.Args)
		r.b.WriteByte(')')
	case ExprSubquery:
		r.b.WriteByte('(')
		r.renderSelect(e.Stmt)
		r.b.WriteByte(')')
	case ExprList:
		r.b.WriteByte('(')
		r.renderExprs(e.Items)
		r.b.WriteByte(')')
	case ExprCast:
		r.b.WriteString("CAST(")
		r.renderExpr(*e.Left)
		r.b.WriteString(" AS ")
		r.b.WriteString(e.TypeName)
		r.b.WriteByte(')')
	case ExprBetween:
		r.renderExpr(*e.Left)
		r.b.WriteString(" BETWEEN ")
		r.renderExpr(*e.Low)
		r.b.WriteString(" AND ")
		r.renderExpr(*e.High)
	case ExprIn:
		r.renderExpr(*e.Left)
		r.b.WriteString(" IN (")
		if e.Stmt != nil {
			r.renderSelect(e.Stmt)
		} else {
			r.renderExprs(e.Items)
		}
		r.b.WriteByte(')')
	case ExprIsNull:
		r.renderExpr(*e.Left)
		r.b.WriteString(" IS NULL")
	case ExprNotNull:
		r.renderExpr(*e.Left)
		r.b.WriteString(" IS NOT NULL")
	case ExprExists:
		r.b.WriteString("EXISTS (")
		r.renderSelect(e.Stmt)
		r.b.WriteByte(')')
	case ExprStar:
		r.b.WriteByte('*')
	}
}

func (r *renderer) renderBinary(e Expr) {
	// AND/OR — lower precedence, wrap in parens
	needParen := e.Op == "AND" || e.Op == "OR"
	if needParen {
		r.b.WriteByte('(')
		r.renderExpr(*e.Left)
		r.b.WriteString(" ")
		r.b.WriteString(e.Op)
		r.b.WriteString(" ")
		r.renderExpr(*e.Right)
		r.b.WriteByte(')')
	} else {
		r.renderExpr(*e.Left)
		r.b.WriteString(" ")
		r.b.WriteString(e.Op)
		r.b.WriteString(" ")
		r.renderExpr(*e.Right)
	}
}
