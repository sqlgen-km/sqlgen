package ast

import "testing"

func TestRenderPG(t *testing.T) {
	// SELECT
	s := &SelectStmt{
		Columns: []Expr{Col("id"), Col("display_name")},
		From:    TableRef{Name: "users"},
		Where:   Eq(Col("id"), Param("id")),
		OrderBy: []OrderClause{{Expr: Col("id"), Desc: true}},
		Limit:   &LimitClause{Count: Param("limit"), Offset: Param("offset")},
	}
	sql := PG.Render(s)
	want := "SELECT id, display_name FROM users WHERE id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3"
	if sql != want {
		t.Errorf("PG SELECT:\n got  %s\n want %s", sql, want)
	}

	// INSERT with RETURNING
	ins := &InsertStmt{
		Table:   TableRef{Name: "users"},
		Columns: []string{"display_name", "gender"},
		Values:  [][]Expr{{Param("display_name"), Param("gender")}},
		Returning: []string{"id"},
	}
	sql = PG.Render(ins)
	want = "INSERT INTO users (display_name, gender) VALUES ($1, $2) RETURNING id"
	if sql != want {
		t.Errorf("PG INSERT:\n got  %s\n want %s", sql, want)
	}

	// JOIN
	js := &SelectStmt{
		Columns: []Expr{Col("u.id"), Col("o.total")},
		From:    TableRef{Name: "users", Alias: "u"},
		Joins: []JoinClause{
			{Type: LeftJoin, Table: TableRef{Name: "orders", Alias: "o"},
				On: Eq(Col("u.id"), Col("o.user_id"))},
		},
		Where: And(
			*Eq(Col("u.gender"), Param("gender")),
			*IsNull(Col("u.deleted_at")),
		),
	}
	sql = PG.Render(js)
	want = "SELECT u.id, o.total FROM users u LEFT JOIN orders o ON u.id = o.user_id WHERE (u.gender = $1 AND u.id IS NULL)" // wait, no - "u.deleted_at IS NULL"
	// Actually re-check...
	// Where: AND(Eq(u.gender, $gender), IsNull(u.deleted_at))
	// Parameters: gender → $1. deleted_at IS NULL has no param.
	want = "SELECT u.id, o.total FROM users u LEFT JOIN orders o ON u.id = o.user_id WHERE (u.gender = $1 AND u.deleted_at IS NULL)"
	if sql != want {
		t.Errorf("PG JOIN:\n got  %s\n want %s", sql, want)
	}
}

func TestRenderOracle(t *testing.T) {
	// SELECT with LIMIT/OFFSET
	s := &SelectStmt{
		Columns: []Expr{Col("id"), Col("display_name")},
		From:    TableRef{Name: "users"},
		Where:   Eq(Col("id"), Param("id")),
		OrderBy: []OrderClause{{Expr: Col("id"), Desc: true}},
		Limit:   &LimitClause{Count: Param("limit"), Offset: Param("offset")},
	}
	sql := Ora.Render(s)
	want := "SELECT id, display_name FROM users WHERE id = :1 ORDER BY id DESC OFFSET :2 ROWS FETCH NEXT :3 ROWS ONLY"
	if sql != want {
		t.Errorf("Oracle SELECT:\n got  %s\n want %s", sql, want)
	}
}
