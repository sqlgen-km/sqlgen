package main

import (
	"fmt"

	"github.com/sqlgen-km/sqlgen/languages/java"
	"github.com/sqlgen-km/sqlgen/languages/java/mssql"
	"github.com/sqlgen-km/sqlgen/languages/java/mysql"
	javaora "github.com/sqlgen-km/sqlgen/languages/java/oracle"
	javapg "github.com/sqlgen-km/sqlgen/languages/java/pg"
)

// javaEngineRegistry maps engine names to Java engine constructors.
var javaEngineRegistry = map[string]func() java.Engine{
	"pg":     func() java.Engine { e := javapg.New(); return &e },
	"mysql":  func() java.Engine { e := mysql.New(); return &e },
	"oracle": func() java.Engine { e := javaora.New(); return &e },
	"mssql":  func() java.Engine { e := mssql.New(); return &e },
}

func getJavaEngine(name string) (java.Engine, error) {
	fn, ok := javaEngineRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown Java engine %q", name)
	}
	return fn(), nil
}
