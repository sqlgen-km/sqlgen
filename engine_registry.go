package main

import (
	"fmt"

	"github.com/sqlgen-km/sqlgen/engines"
	"github.com/sqlgen-km/sqlgen/languages/golang/mssql"
	"github.com/sqlgen-km/sqlgen/languages/golang/mysql"
	"github.com/sqlgen-km/sqlgen/languages/golang/oracle"
	"github.com/sqlgen-km/sqlgen/languages/golang/pg"
)

// engineRegistry maps engine names to constructors.
var engineRegistry = map[string]func() engines.Engine{
	"pg":     func() engines.Engine { e := pg.New(); return &e },
	"mysql":  func() engines.Engine { e := mysql.New(); return &e },
	"mssql":  func() engines.Engine { e := mssql.New(); return &e },
	"oracle": func() engines.Engine { e := oracle.New(); return &e },
}

// getEngine returns an engine by name.
func getEngine(name string) (engines.Engine, error) {
	fn, ok := engineRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown engine %q", name)
	}
	return fn(), nil
}
