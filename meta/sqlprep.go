package meta

import (
	"regexp"
	"strings"
)

// SQLPrep holds the result of SQL preprocessing.
type SQLPrep struct {
	CleanedSQL string
	Params     []ParamRef
	Returning  []string
	HasStar    bool
	HasILIKE   bool
	OnConflict *OnConflictInfo
}

// PreprocessSQL extracts RETURNING/ON CONFLICT, replaces @param references and ILIKE.
func PreprocessSQL(rawSQL string) *SQLPrep {
	p := &SQLPrep{}
	sql := strings.TrimSpace(rawSQL)
	sql = p.extractReturning(sql)
	sql = p.extractOnConflict(sql)
	sql = p.replaceILIKE(sql)
	sql = p.replaceParams(sql)
	p.CleanedSQL = sql
	return p
}

var returningRe = regexp.MustCompile(`(?i)\s+RETURNING\s+(\*|(?:\w+\s*,\s*)*\w+)\s*$`)

func (p *SQLPrep) extractReturning(sql string) string {
	m := returningRe.FindStringSubmatch(sql)
	if m == nil {
		return sql
	}
	cols := m[1]
	if cols == "*" {
		p.HasStar = true
	} else {
		for _, part := range strings.Split(cols, ",") {
			p.Returning = append(p.Returning, strings.TrimSpace(part))
		}
	}
	return returningRe.ReplaceAllString(sql, "")
}

var onConflictRe = regexp.MustCompile(`(?si)\s+ON\s+CONFLICT\s+\(([^)]+)\)\s+DO\s+(NOTHING|UPDATE\s+SET\s+(.+?))(?:\s+RETURNING\s+.+)?$`)

func (p *SQLPrep) extractOnConflict(sql string) string {
	m := onConflictRe.FindStringSubmatch(sql)
	if m == nil {
		return sql
	}
	info := &OnConflictInfo{}
	for _, c := range strings.Split(m[1], ",") {
		info.Columns = append(info.Columns, strings.TrimSpace(c))
	}
	if m[2] == "NOTHING" {
		info.DoUpdate = false
	} else {
		info.DoUpdate = true
		setsStr := strings.TrimSpace(m[3])
		for _, set := range strings.Split(setsStr, ",") {
			parts := strings.SplitN(strings.TrimSpace(set), "=", 2)
			if len(parts) == 2 {
				info.Sets = append(info.Sets, SetClauseInfo{
					Col: strings.TrimSpace(parts[0]),
					Val: strings.TrimSpace(parts[1]),
				})
			}
		}
	}
	p.OnConflict = info
	return onConflictRe.ReplaceAllString(sql, "")
}

var paramRe = regexp.MustCompile(`@(\w+(?:\.\w+)?)`)

var ilikeRe = regexp.MustCompile(`(?i)\s+ILIKE\s+`)

func (p *SQLPrep) replaceILIKE(sql string) string {
	result := ilikeRe.ReplaceAllString(sql, " LIKE ")
	if result != sql {
		p.HasILIKE = true
	}
	return result
}

func (p *SQLPrep) replaceParams(sql string) string {
	var params []ParamRef

	result := paramRe.ReplaceAllStringFunc(sql, func(match string) string {
		name := match[1:] // strip @

		ref := ParamRef{Full: name}
		if dotIdx := strings.IndexByte(name, '.'); dotIdx >= 0 {
			ref.Param = name[:dotIdx]
			ref.Field = name[dotIdx+1:]
			ref.IsField = true
		} else {
			ref.Param = name
			ref.Field = name
		}
		params = append(params, ref)
		return "?"
	})

	p.Params = params
	return result
}
