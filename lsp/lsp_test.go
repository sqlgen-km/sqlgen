package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const sampleValid = `-- package: demo

-- model: User { id int64, name string }

-- param: id int64
-- name: FindByID :one
-- model: User
SELECT id, name FROM users WHERE id = @id
`

const sampleInvalid = `-- package: demo

-- name: FindByID :one
SELECT id FROM users WHERE id = @id
`

// ── feature unit tests ──

func TestDiagnosticsValid(t *testing.T) {
	if got := computeDiagnostics("file:///demo/basic.sql", sampleValid); len(got) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", got)
	}
}

func TestDiagnosticsInvalid(t *testing.T) {
	got := computeDiagnostics("file:///demo/basic.sql", sampleInvalid)
	if len(got) == 0 {
		t.Fatal("expected diagnostics for invalid file")
	}
	if got[0].Severity != SeverityError || got[0].Source != "sqlgen" {
		t.Fatalf("unexpected diagnostic: %+v", got[0])
	}
}

func TestDiagnosticsINConstraint(t *testing.T) {
	// IN (@x) single param must be flagged by BuildQuery.
	text := `-- package: demo

-- param: ids []int64
-- name: FindByIDs :many
-- model: User
SELECT id FROM users WHERE id IN (@ids)
`
	got := computeDiagnostics("file:///demo/x.sql", text)
	if len(got) == 0 {
		t.Fatal("expected IN(@x) diagnostic")
	}
	if !strings.Contains(got[0].Message, "ANY") {
		t.Fatalf("expected ANY hint in message, got %q", got[0].Message)
	}
}

func TestCompletionsDirectives(t *testing.T) {
	items := computeCompletions("file:///x.sql", sampleValid, position{Line: 1, Character: 0})
	// line 1 is blank → directive completions
	if len(items) == 0 {
		t.Fatal("expected directive completions on blank line")
	}
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.Label] = true
	}
	for _, want := range []string{"package", "model", "param", "name", "@"} {
		if !labels[want] {
			t.Errorf("missing directive completion %q", want)
		}
	}
}

func TestCompletionsMode(t *testing.T) {
	// "-- name: FindByID :" — cursor at end of that line.
	text := sampleValid
	lines := splitLines(text)
	idx := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "-- name: FindByID") {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("sample missing name line")
	}
	items := computeCompletions("file:///x.sql", text, position{Line: idx, Character: len(lines[idx])})
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.Label] = true
	}
	for _, want := range []string{"one", "many", "exec", "execrows"} {
		if !labels[want] {
			t.Errorf("missing mode completion %q", want)
		}
	}
}

func TestDefinitionModelRef(t *testing.T) {
	// Cursor on "-- model: User" reference line (not the definition).
	lines := splitLines(sampleValid)
	refLine := -1
	for i, l := range lines {
		if l == "-- model: User" {
			refLine = i
		}
	}
	if refLine < 0 {
		t.Fatal("sample missing model ref line")
	}
	locs := computeDefinition("file:///x.sql", sampleValid, position{Line: refLine, Character: 5})
	if len(locs) == 0 {
		t.Fatal("expected model definition location")
	}
	// Definition line is "-- model: User {" which is before the ref.
	if locs[0].Range.Start.Line >= refLine {
		t.Fatalf("definition line %d should be before ref %d", locs[0].Range.Start.Line, refLine)
	}
}

func TestDocumentSymbols(t *testing.T) {
	syms := computeDocumentSymbols("file:///x.sql", sampleValid)
	kinds := map[string]int{}
	for _, s := range syms {
		kinds[s.Name] = s.Kind
	}
	if kinds["User"] != symbolStruct {
		t.Errorf("User should be a struct symbol, got %d", kinds["User"])
	}
	if kinds["FindByID"] != symbolMethod {
		t.Errorf("FindByID should be a method symbol, got %d", kinds["FindByID"])
	}
}

func TestGeneratePreview(t *testing.T) {
	preview, err := generatePreview("file:///demo/basic.sql", sampleValid)
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !strings.Contains(preview, "=== Go ===") || !strings.Contains(preview, "=== Java ===") {
		t.Fatalf("preview missing sections:\n%s", preview)
	}
	if !strings.Contains(preview, "FindByID") {
		t.Fatalf("preview missing generated method:\n%s", preview)
	}
}

// ── server integration test ──

func frameJSON(t *testing.T, id interface{}, method string, params interface{}) []byte {
	t.Helper()
	body := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if id != nil {
		body["id"] = id
	}
	if params != nil {
		body["params"] = params
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))), data...)
}

func parseFrames(t *testing.T, data []byte) []map[string]json.RawMessage {
	t.Helper()
	var frames []map[string]json.RawMessage
	for len(data) > 0 {
		idx := bytes.Index(data, []byte("\r\n\r\n"))
		if idx < 0 {
			t.Fatalf("no header terminator in output: %q", data)
		}
		var n int
		fmt.Sscanf(string(data[:idx]), "Content-Length: %d", &n)
		bodyStart := idx + 4
		if bodyStart+n > len(data) {
			t.Fatalf("truncated frame: want %d bytes, have %d", n, len(data)-bodyStart)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data[bodyStart:bodyStart+n], &m); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, m)
		data = data[bodyStart+n:]
	}
	return frames
}

func TestServerEndToEnd(t *testing.T) {
	var in bytes.Buffer
	in.Write(frameJSON(t, 1, "initialize", map[string]interface{}{}))
	in.Write(frameJSON(t, nil, "initialized", map[string]interface{}{}))
	in.Write(frameJSON(t, nil, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file:///demo/basic.sql", "languageId": "sqlgen", "version": 1, "text": sampleValid,
		},
	}))
	in.Write(frameJSON(t, 2, "textDocument/completion", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": "file:///demo/basic.sql"},
		"position":     map[string]interface{}{"line": 1, "character": 0},
	}))
	in.Write(frameJSON(t, 3, "textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": "file:///demo/basic.sql"},
		"position":     map[string]interface{}{"line": 6, "character": 5},
	}))
	in.Write(frameJSON(t, 4, "textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": "file:///demo/basic.sql"},
	}))
	in.Write(frameJSON(t, 5, "workspace/executeCommand", map[string]interface{}{
		"command":   "sqlgen.generatePreview",
		"arguments": []string{"file:///demo/basic.sql"},
	}))
	in.Write(frameJSON(t, 6, "shutdown", nil))
	in.Write(frameJSON(t, nil, "exit", nil))

	var out bytes.Buffer
	s := &server{in: &in, out: &out, docs: map[string]string{}, version: "test"}
	if err := s.serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	frames := parseFrames(t, out.Bytes())

	results := map[string]json.RawMessage{}
	sawDiagnostics := false
	for _, f := range frames {
		if method, ok := f["method"]; ok && string(method) == `"textDocument/publishDiagnostics"` {
			sawDiagnostics = true
			continue
		}
		if id, ok := f["id"]; ok {
			results[string(id)] = f["result"]
		}
	}

	if !sawDiagnostics {
		t.Error("expected publishDiagnostics notification")
	}
	for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
		if _, ok := results[id]; !ok {
			t.Errorf("missing response for id %s", id)
		}
	}

	// initialize result has capabilities
	var init initializeResult
	if err := json.Unmarshal(results["1"], &init); err != nil {
		t.Fatal(err)
	}
	if init.Capabilities.TextDocumentSync != 1 || init.Capabilities.ExecuteCommandProvider == nil {
		t.Errorf("unexpected initialize capabilities: %+v", init.Capabilities)
	}

	// completion has items
	var comp completionList
	if err := json.Unmarshal(results["2"], &comp); err != nil {
		t.Fatal(err)
	}
	if len(comp.Items) == 0 {
		t.Error("expected completion items")
	}

	// definition has locations
	var defs []location
	if err := json.Unmarshal(results["3"], &defs); err != nil {
		t.Fatal(err)
	}
	if len(defs) == 0 {
		t.Error("expected definition locations")
	}

	// symbols
	var syms []documentSymbol
	if err := json.Unmarshal(results["4"], &syms); err != nil {
		t.Fatal(err)
	}
	if len(syms) == 0 {
		t.Error("expected document symbols")
	}

	// preview
	var preview map[string]string
	if err := json.Unmarshal(results["5"], &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview["preview"], "=== Go ===") {
		t.Errorf("preview missing Go section")
	}

	// shutdown returns null
	if string(results["6"]) != "null" {
		t.Errorf("shutdown should return null, got %s", results["6"])
	}
}
