// Package java provides Java/MyBatis code generation for sqlgen.
package java

import "github.com/sqlgen-km/sqlgen/engines"

// Engine is the interface for Java/MyBatis code generation engines.
// Each implementation handles one database dialect (PG, MySQL, Oracle, MSSQL),
// producing MyBatis-annotated mapper interface method bodies.
type Engine interface {
	// Name returns the engine identifier ("pg", "mysql", "oracle", "mssql").
	Name() string

	// Profile returns the Spring profile name ("pg", "mysql", "oracle", "mssql").
	Profile() string

	// DriverName returns the JDBC driver name for the factory switch:
	// "postgresql", "mysql", "oracle", "sqlserver".
	DriverName() string

	// GenMapper generates the body of a MyBatis Mapper implementation interface
	// for the given file stem, runner specs, and primary model type. Output is the
	// full interface definition (without package/import header).
	GenMapper(stem string, specs []engines.RunnerSpec, modelType string) string
}
