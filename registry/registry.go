// Package registry provides engine construction shared by the CLI and LSP server.
package registry

import (
	"fmt"

	"github.com/sqlgen-km/sqlgen/engines"
	gomssql "github.com/sqlgen-km/sqlgen/languages/golang/mssql"
	gomysql "github.com/sqlgen-km/sqlgen/languages/golang/mysql"
	gooracle "github.com/sqlgen-km/sqlgen/languages/golang/oracle"
	gopg "github.com/sqlgen-km/sqlgen/languages/golang/pg"
	"github.com/sqlgen-km/sqlgen/languages/java"
	javamssql "github.com/sqlgen-km/sqlgen/languages/java/mssql"
	javamysql "github.com/sqlgen-km/sqlgen/languages/java/mysql"
	javaoracle "github.com/sqlgen-km/sqlgen/languages/java/oracle"
	javapg "github.com/sqlgen-km/sqlgen/languages/java/pg"
)

var goRegistry = map[string]func() engines.Engine{
	"pg":     func() engines.Engine { e := gopg.New(); return &e },
	"mysql":  func() engines.Engine { e := gomysql.New(); return &e },
	"mssql":  func() engines.Engine { e := gomssql.New(); return &e },
	"oracle": func() engines.Engine { e := gooracle.New(); return &e },
}

var javaRegistry = map[string]func() java.Engine{
	"pg":     func() java.Engine { e := javapg.New(); return &e },
	"mysql":  func() java.Engine { e := javamysql.New(); return &e },
	"mssql":  func() java.Engine { e := javamssql.New(); return &e },
	"oracle": func() java.Engine { e := javaoracle.New(); return &e },
}

// GetEngine returns a fresh Go engine instance by name.
func GetEngine(name string) (engines.Engine, error) {
	fn, ok := goRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown engine %q", name)
	}
	return fn(), nil
}

// GetJavaEngine returns a fresh Java engine instance by name.
func GetJavaEngine(name string) (java.Engine, error) {
	fn, ok := javaRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown Java engine %q", name)
	}
	return fn(), nil
}

// GoEngines returns a fresh instance of every Go engine keyed by name.
func GoEngines() map[string]engines.Engine {
	out := make(map[string]engines.Engine, len(goRegistry))
	for name, fn := range goRegistry {
		out[name] = fn()
	}
	return out
}

// JavaEngines returns a fresh instance of every Java engine keyed by name.
func JavaEngines() map[string]java.Engine {
	out := make(map[string]java.Engine, len(javaRegistry))
	for name, fn := range javaRegistry {
		out[name] = fn()
	}
	return out
}

// EngineNames returns the canonical dialect names in deterministic order.
func EngineNames() []string {
	return []string{"pg", "mysql", "mssql", "oracle"}
}
