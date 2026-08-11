// Package meta provides shared types used by DSL parsing and code generation.
package meta

// ParsedFile is a fully parsed .sql file.
type ParsedFile struct {
	Package string
	Models  []ModelDef
	Queries []QueryDef
}

// ModelDef is a model struct declaration.
type ModelDef struct {
	Name   string
	Fields []FieldDef
}

// FieldDef is a single field in a model.
type FieldDef struct {
	Name string
	Type string
}

// QueryDef is a single query method definition.
type QueryDef struct {
	Name      string        // method name
	Mode      string        // "one", "many", "exec"
	Params    []ParamDef
	Return    string        // return type name
	IsScalar  bool          // true for -- model int64 shorthand
	FieldMaps []FieldMapDef // field→column mapping for return type
	Doc       []string      // -- @ doc comment lines
	SQL       string
	Src       string
	Line      int
}

// ParamDef is a single input parameter.
type ParamDef struct {
	Name string
	Type string
}

// FieldMapDef maps a SQL column to a Go field name.
type FieldMapDef struct {
	Column string // SQL column name
	Field  string // Go field name
}

// OnConflictInfo holds parsed ON CONFLICT clause information.
type OnConflictInfo struct {
	Columns  []string
	DoUpdate bool
	Sets     []SetClauseInfo
}

// SetClauseInfo holds a single SET clause in ON CONFLICT DO UPDATE.
type SetClauseInfo struct {
	Col string
	Val string
}

// ParamRef records how a DSL @reference maps to function args.
type ParamRef struct {
	Full    string // e.g. "filter.gender" or "id"
	Field   string // field name: "gender" or "id"
	Param   string // function param name: "filter" or "id"
	IsField bool   // true if this is param.field access
}

// JavaPkgCfg is a single Java package configuration.
type JavaPkgCfg struct {
	ModelPackage  string   `yaml:"modelPackage"`
	MapperPackage string   `yaml:"mapperPackage"`
	Out           string   `yaml:"out"`
	Files         []string `yaml:"files"`
}
