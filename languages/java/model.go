package java

import (
	"fmt"
	"strings"

	"github.com/sqlgen-km/sqlgen/engines"
)

// GenModel generates a Java Record class from a model definition.
func GenModel(model engines.Model) string {
	var b strings.Builder

	// Class javadoc
	b.WriteString("/** Auto-generated model for {@code ")
	b.WriteString(model.Name)
	b.WriteString("}. */\n")

	// Record declaration
	b.WriteString("public record ")
	b.WriteString(model.Name)
	b.WriteString("(\n")

	// Fields
	for i, f := range model.Fields {
		javaType := go2javaType(f.Type)
		comma := ","
		if i == len(model.Fields)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "    %s %s%s\n", javaType, javaFieldName(f.Name), comma)
	}

	b.WriteString(") {}\n")
	return b.String()
}

// GenModelImports returns the necessary import statements for a set of models.
func GenModelImports(models []engines.Model) string {
	needsMath := false
	needsTime := false

	for _, m := range models {
		for _, f := range m.Fields {
			if goTypeNeedsMath(f.Type) {
				needsMath = true
			}
			if goTypeNeedsImport(f.Type) {
				needsTime = true
			}
		}
	}

	var b strings.Builder
	if needsMath {
		b.WriteString("import java.math.BigDecimal;\n")
	}
	if needsTime {
		b.WriteString("import java.time.LocalDateTime;\n")
	}
	return b.String()
}

// LowerFirst converts a PascalCase name to camelCase for Java identifiers.
// "ID" → "id", "URLParser" → "urlParser", "FindByID" → "findByID", "Item" → "item".
func LowerFirst(name string) string {
	if name == "" {
		return name
	}
	b := []byte(name)
	// If first two chars are uppercase ASCII, handle acronym prefix
	if len(b) > 1 && b[0] >= 'A' && b[0] <= 'Z' && b[1] >= 'A' && b[1] <= 'Z' {
		// Find the end of the uppercase prefix
		end := 1
		for end < len(b) && b[end] >= 'A' && b[end] <= 'Z' {
			end++
		}
		// If a lowercase char follows, keep the last uppercase char capitalized
		if end < len(b) && b[end] >= 'a' && b[end] <= 'z' {
			end--
		}
		prefix := strings.ToLower(string(b[:end]))
		return prefix + string(b[end:])
	}
	// Standard camelCase: lowercase first character
	return strings.ToLower(name[:1]) + name[1:]
}

// javaFieldName is an alias for LowerFirst, kept for local use in model generation.
func javaFieldName(name string) string {
	return LowerFirst(name)
}
