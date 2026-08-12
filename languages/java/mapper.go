package java

import (
	"fmt"
	"strings"

	"github.com/sqlgen-km/sqlgen/engines"
)

// GenSharedMapper generates the shared mapper interface (no annotations, just signatures).
func GenSharedMapper(mapperName string, specs []engines.RunnerSpec, modelType string) string {
	var b strings.Builder

	b.WriteString("/** Shared Mapper interface. */\n")
	b.WriteString("public interface ")
	b.WriteString(mapperName)
	b.WriteString(" {\n")

	for _, spec := range specs {
		b.WriteString("\n    ")
		genMapperMethodSig(&b, spec, modelType)
		b.WriteString("\n")
	}

	b.WriteString("}\n")
	return b.String()
}

// genMapperMethodSig generates a single method signature for the shared interface.
func genMapperMethodSig(b *strings.Builder, spec engines.RunnerSpec, modelType string) {
	mt := modelType
	if spec.ModelType != "" {
		mt = spec.ModelType
	}
	retType := MethodReturnType(spec, mt)
	methodName := LowerFirst(spec.Query)

	fmt.Fprintf(b, "%s %s(", retType, methodName)

	if spec.Kind == engines.RunnerReturningScalar {
		// INSERT RETURNING: single object parameter carrying the generated key
		fmt.Fprintf(b, "%s %s", InsertParamType(spec), InsertParamArg(spec))
	} else {
		for i, p := range spec.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			javaType := go2javaType(p.Type)
			fmt.Fprintf(b, "@Param(\"%s\") %s %s", p.Name, javaType, p.Name)
		}
	}

	b.WriteString(");")
}

// GenMapperImports generates the import block for a mapper file.
func GenMapperImports(specs []engines.RunnerSpec) string {
	var b strings.Builder

	needsList := false
	needsParam := false
	needsTime := false
	needsMath := false

	for _, spec := range specs {
		if spec.Kind == engines.RunnerQueryMany {
			needsList = true
		}
		for _, p := range spec.Params {
			if goTypeNeedsImport(p.Type) {
				needsTime = true
			}
			if goTypeNeedsMath(p.Type) {
				needsMath = true
			}
		}
		if len(spec.Params) > 0 {
			needsParam = true
		}
	}

	if needsList {
		b.WriteString("import java.util.List;\n")
	}
	if needsTime {
		b.WriteString("import java.time.LocalDateTime;\n")
	}
	if needsMath {
		b.WriteString("import java.math.BigDecimal;\n")
	}
	b.WriteString("\n")
	if needsParam {
		b.WriteString("import org.apache.ibatis.annotations.Param;\n")
	}
	b.WriteString("import org.apache.ibatis.annotations.*;\n")
	b.WriteString("import org.springframework.context.annotation.Profile;\n")
	b.WriteString("\n")

	return b.String()
}

// GenEngineMapperHeader generates the start of a per-engine mapper implementation interface.
func GenEngineMapperHeader(mapperName string, profile string, suffix string) string {
	var b strings.Builder

	b.WriteString("@Mapper\n")
	fmt.Fprintf(&b, "@Profile(\"%s\")\n", profile)
	fmt.Fprintf(&b, "public interface %s%s extends %s {\n", mapperName, suffix, mapperName)

	return b.String()
}

// GenEngineMapperFooter generates the closing brace.
func GenEngineMapperFooter() string {
	return "}\n"
}

// ToPascal converts a snake_case or kebab-case name to PascalCase.
func ToPascal(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
