package ast

// -------------------- Statement Types --------------------

// Statement is the interface for all SQL statement AST nodes.
type Statement interface {
	statementNode()
}

// SelectStmt represents a SELECT statement.
type SelectStmt struct {
	Distinct  bool
	Columns   []Expr
	From      TableRef
	Joins     []JoinClause
	Where     *Expr
	GroupBy   []Expr
	Having    *Expr
	OrderBy   []OrderClause
	Limit     *LimitClause
	ForUpdate bool
}

func (*SelectStmt) statementNode() {}

// InsertStmt represents an INSERT statement.
type InsertStmt struct {
	Table      TableRef
	Columns    []string
	Values     [][]Expr    // multi-row VALUES
	Select     *SelectStmt // INSERT ... SELECT
	Returning  []string
	OnConflict *OnConflict
}

type OnConflict struct {
	Columns  []string
	DoUpdate bool
	Sets     []SetClause
}

func (*InsertStmt) statementNode() {}

// UpdateStmt represents an UPDATE statement.
type UpdateStmt struct {
	Table     TableRef
	Sets      []SetClause
	From      []TableRef // UPDATE ... FROM (PG extension)
	Where     *Expr
	Returning []string
}

func (*UpdateStmt) statementNode() {}

// DeleteStmt represents a DELETE statement.
type DeleteStmt struct {
	Table     TableRef
	Using     []TableRef // DELETE ... USING (PG extension)
	Where     *Expr
	Returning []string
}

func (*DeleteStmt) statementNode() {}

// -------------------- Clauses --------------------

// TableRef is a table reference, optionally aliased.
type TableRef struct {
	Name   string
	Alias  string
	Quoted bool
}

// JoinClause represents a JOIN clause.
type JoinClause struct {
	Type  JoinType
	Table TableRef
	On    *Expr  // ON condition
	Using []string // USING (cols) — mutually exclusive with On
}

// JoinType is the type of JOIN.
type JoinType int

const (
	InnerJoin JoinType = iota
	LeftJoin
	RightJoin
	CrossJoin
)

func (j JoinType) String() string {
	switch j {
	case InnerJoin:
		return "INNER JOIN"
	case LeftJoin:
		return "LEFT JOIN"
	case RightJoin:
		return "RIGHT JOIN"
	case CrossJoin:
		return "CROSS JOIN"
	default:
		return "JOIN"
	}
}

// SetClause is a SET col = val pair for UPDATE.
type SetClause struct {
	Col string
	Val Expr
}

// OrderClause is an ORDER BY entry.
type OrderClause struct {
	Expr       Expr
	Desc       bool
	NullsFirst bool
	NullsLast  bool
}

// LimitClause is a LIMIT / OFFSET clause.
type LimitClause struct {
	Count  Expr
	Offset Expr // zero-value Expr means no OFFSET
}

// -------------------- Expressions --------------------

// ExprKind identifies the type of expression.
type ExprKind int

const (
	ExprCol      ExprKind = iota // column reference
	ExprLiteral                   // literal value
	ExprParam                     // named parameter
	ExprBinary                    // binary operation
	ExprUnary                     // unary operation
	ExprCall                      // function call
	ExprSubquery                  // subquery
	ExprList                      // list of expressions
	ExprCast                      // type cast
	ExprBetween                   // x BETWEEN low AND high
	ExprIn                        // x IN (...)
	ExprIsNull                    // x IS NULL
	ExprNotNull                   // x IS NOT NULL
	ExprExists                    // EXISTS (subquery)
	ExprStar                      // *
	ExprAny                       // x = ANY(@arr) — array membership (DSL sugar)
)

// Expr is an expression node.
//
// Fields used per Kind:
//
//	ExprCol:      Col
//	ExprLiteral:  Val
//	ExprParam:    Param
//	ExprBinary:   Op, Left, Right
//	ExprUnary:    Op, Left
//	ExprCall:     Name, Args
//	ExprSubquery: Stmt
//	ExprList:     Items
//	ExprCast:     Left, TypeName
//	ExprBetween:  Left, Low, High
//	ExprIn:       Left, Items (or Stmt for subquery)
//	ExprIsNull:   Left
//	ExprNotNull:  Left
//	ExprExists:   Stmt
//	ExprStar:     (none)
//	ExprAny:      Left (value being tested), Right (array param expr)
type Expr struct {
	Kind ExprKind

	// Column reference
	Col    string
	Alias  string // SELECT ... AS alias (empty if no alias)
	Quoted bool   // true if the column/table was double-quoted in DSL

	// Literal value (string, int, float, bool, nil for NULL)
	Val any

	// Named parameter
	Param string

	// Binary/Unary operator
	Op    string
	Left  *Expr
	Right *Expr

	// Function call
	Name string
	Args []Expr

	// Subquery
	Stmt *SelectStmt

	// List items (for IN lists, row values, etc.)
	Items []Expr

	// Type cast
	TypeName string

	// BETWEEN low and high
	Low  *Expr
	High *Expr
}

// hasOffset reports whether LimitClause has a non-zero OFFSET.
func (l *LimitClause) hasOffset() bool {
	return l.Offset.Kind != 0
}

// -------------------- Expr Constructors --------------------

// Col creates a column reference expression.
func Col(name string) Expr { return Expr{Kind: ExprCol, Col: name} }

// Param creates a named parameter expression.
func Param(name string) Expr { return Expr{Kind: ExprParam, Param: name} }

// Lit creates a literal value expression.
func Lit(v any) Expr { return Expr{Kind: ExprLiteral, Val: v} }

// Star creates a * expression (SELECT *).
func Star() Expr { return Expr{Kind: ExprStar} }

// Ptr returns a pointer to e. Useful for *Expr fields.
func Ptr(e Expr) *Expr { return &e }

// --- Binary ---

// And creates a left AND right expression.
func And(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "AND", Left: &left, Right: &right}
}

// Or creates a left OR right expression.
func Or(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "OR", Left: &left, Right: &right}
}

// Eq creates a left = right expression.
func Eq(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "=", Left: &left, Right: &right}
}

// Ne creates a left <> right expression.
func Ne(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "<>", Left: &left, Right: &right}
}

// Lt creates a left < right expression.
func Lt(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "<", Left: &left, Right: &right}
}

// Gt creates a left > right expression.
func Gt(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: ">", Left: &left, Right: &right}
}

// Le creates a left <= right expression.
func Le(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "<=", Left: &left, Right: &right}
}

// Ge creates a left >= right expression.
func Ge(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: ">=", Left: &left, Right: &right}
}

// Like creates a left LIKE right expression.
func Like(left, right Expr) *Expr {
	return &Expr{Kind: ExprBinary, Op: "LIKE", Left: &left, Right: &right}
}

// --- Unary ---

// Not creates a NOT x expression.
func Not(x Expr) *Expr { return &Expr{Kind: ExprUnary, Op: "NOT", Left: &x} }

// Neg creates a -x expression.
func Neg(x Expr) *Expr { return &Expr{Kind: ExprUnary, Op: "-", Left: &x} }

// --- NULL checks ---

// IsNull creates an x IS NULL expression.
func IsNull(x Expr) *Expr { return &Expr{Kind: ExprIsNull, Left: &x} }

// NotNull creates an x IS NOT NULL expression.
func NotNull(x Expr) *Expr { return &Expr{Kind: ExprNotNull, Left: &x} }

// --- IN / BETWEEN ---

// In creates an x IN (items...) expression.
func In(x Expr, items ...Expr) *Expr {
	return &Expr{Kind: ExprIn, Left: &x, Items: items}
}

// InSubquery creates an x IN (subquery) expression.
func InSubquery(x Expr, sub *SelectStmt) *Expr {
	return &Expr{Kind: ExprIn, Left: &x, Stmt: sub}
}

// Any creates an x = ANY(array) array-membership expression.
// left is the value being tested (e.g. a column), right is the array param expr.
func Any(left, right Expr) *Expr {
	return &Expr{Kind: ExprAny, Left: &left, Right: &right}
}

// Between creates an x BETWEEN low AND high expression.
func Between(x, low, high Expr) *Expr {
	return &Expr{Kind: ExprBetween, Left: &x, Low: &low, High: &high}
}

// --- Function calls ---

// Call creates a function call expression.
func Call(name string, args ...Expr) Expr {
	return Expr{Kind: ExprCall, Name: name, Args: args}
}

// CallP is like Call but returns *Expr for use in pointer contexts.
func CallP(name string, args ...Expr) *Expr {
	e := Call(name, args...)
	return &e
}

// --- Subquery ---

// Subquery creates a subquery expression.
func Subquery(stmt *SelectStmt) Expr {
	return Expr{Kind: ExprSubquery, Stmt: stmt}
}

// --- Type cast ---

// Cast creates a CAST(x AS typ) expression.
func Cast(x Expr, typ string) Expr {
	return Expr{Kind: ExprCast, Left: &x, TypeName: typ}
}

// --- List ---

// List creates an expression list (e.g., for row values).
func List(items ...Expr) Expr {
	return Expr{Kind: ExprList, Items: items}
}
