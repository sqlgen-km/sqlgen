package java

import (
	"fmt"
	"strings"
)

// GenFactory generates the MapperFactory.java static factory class.
func GenFactory(mapperName string, engines []Engine) string {
	var b strings.Builder

	shortName := mapperName

	b.WriteString("import org.apache.ibatis.session.SqlSession;\n\n")

	b.WriteString("/** Factory for creating {@code ")
	b.WriteString(shortName)
	b.WriteString("} instances. */\n")

	b.WriteString("public class ")
	fmt.Fprintf(&b, "%sFactory {\n\n", shortName)

	// Static create method
	b.WriteString("    /**\n")
	b.WriteString("     * Create a {@code ")
	b.WriteString(shortName)
	b.WriteString("} for the given database driver.\n")
	b.WriteString("     * <p>\n")
	b.WriteString("     * Usage without Spring:\n")
	b.WriteString("     * <pre>\n")
	fmt.Fprintf(&b, "     * SqlSession session = sqlSessionFactory.openSession();\n")
	fmt.Fprintf(&b, "     * %s mapper = %sFactory.create(session, \"postgresql\");\n", shortName, shortName)
	b.WriteString("     * </pre>\n")
	b.WriteString("     *\n")
	b.WriteString("     * Valid driver names: ")
	drivers := make([]string, len(engines))
	for i, e := range engines {
		drivers[i] = e.DriverName()
	}
	b.WriteString(strings.Join(drivers, ", "))
	b.WriteString(".\n")
	b.WriteString("     */\n")

	fmt.Fprintf(&b, "    public static %s create(SqlSession session, String driver) {\n", shortName)
	// Generate switch
	if len(engines) == 1 {
		e := engines[0]
		fmt.Fprintf(&b, "        if (\"%s\".equals(driver)) {\n", e.DriverName())
		fmt.Fprintf(&b, "            return session.getMapper(%s%s.class);\n", shortName, suffixFor(e.Name()))
		b.WriteString("        }\n")
	} else {
		b.WriteString("        switch (driver) {\n")
		for _, e := range engines {
			fmt.Fprintf(&b, "            case \"%s\" -> { return session.getMapper(%s%s.class); }\n",
				e.DriverName(), shortName, suffixFor(e.Name()))
		}
		b.WriteString("        }\n")
	}
	b.WriteString("        throw new IllegalArgumentException(\n")
	b.WriteString("            \"Unsupported driver: \" + driver + \". Supported: ")
	b.WriteString(strings.Join(drivers, ", "))
	b.WriteString("\");\n")
	b.WriteString("    }\n")

	b.WriteString("}\n")
	return b.String()
}

func suffixFor(engineName string) string {
	switch engineName {
	case "pg":
		return "PG"
	case "mysql":
		return "MySQL"
	case "oracle":
		return "Oracle"
	case "mssql":
		return "MSSQL"
	default:
		return strings.ToUpper(engineName[:1]) + engineName[1:]
	}
}
