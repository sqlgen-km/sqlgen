package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Run starts the language server on stdin/stdout and blocks until shutdown.
func Run(version string) error {
	return (&server{
		in:      os.Stdin,
		out:     os.Stdout,
		docs:    map[string]string{},
		version: version,
	}).serve()
}

type server struct {
	in      io.Reader
	out     io.Writer
	docs    map[string]string // uri → latest text
	version string
	exited  bool
}

func (s *server) serve() error {
	r := bufio.NewReader(s.in)
	for {
		msg, err := readMessage(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.handle(msg); err != nil {
			fmt.Fprintf(os.Stderr, "lsp: %v\n", err)
		}
		if s.exited {
			return nil
		}
	}
}

// ── framing ──

func readMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			contentLength, _ = strconv.Atoi(v)
		}
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *server) write(data []byte) error {
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err := s.out.Write(data)
	return err
}

func (s *server) respond(id json.RawMessage, result interface{}) error {
	if result == nil {
		result = json.RawMessage("null")
	}
	data, err := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
	}
	return s.write(data)
}

func (s *server) notify(method string, params interface{}) error {
	data, err := json.Marshal(notification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	return s.write(data)
}

// ── dispatch ──

func (s *server) handle(msg []byte) error {
	var req request
	if err := json.Unmarshal(msg, &req); err != nil {
		return err
	}
	if req.Method == "" {
		return nil // a response to a request we never send — ignore
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		return nil
	case "shutdown":
		return s.respond(req.ID, nil)
	case "exit":
		s.exited = true
		return nil
	case "textDocument/didOpen":
		var p didOpenParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		s.docs[p.TextDocument.URI] = p.TextDocument.Text
		s.publishDiagnostics(p.TextDocument.URI)
		return nil
	case "textDocument/didChange":
		var p didChangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		if len(p.ContentChanges) > 0 {
			s.docs[p.TextDocument.URI] = p.ContentChanges[0].Text
		}
		s.publishDiagnostics(p.TextDocument.URI)
		return nil
	case "textDocument/didClose":
		var p didCloseParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		delete(s.docs, p.TextDocument.URI)
		return nil
	case "textDocument/completion":
		var p completionParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		items := computeCompletions(p.TextDocument.URI, s.docs[p.TextDocument.URI], p.Position)
		return s.respond(req.ID, completionList{IsIncomplete: false, Items: items})
	case "textDocument/definition":
		var p definitionParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		return s.respond(req.ID, computeDefinition(p.TextDocument.URI, s.docs[p.TextDocument.URI], p.Position))
	case "textDocument/documentSymbol":
		var p documentSymbolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		return s.respond(req.ID, computeDocumentSymbols(p.TextDocument.URI, s.docs[p.TextDocument.URI]))
	case "workspace/executeCommand":
		var p executeCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return err
		}
		return s.handleExecuteCommand(req, p)
	default:
		return nil
	}
}

func (s *server) handleInitialize(req request) error {
	return s.respond(req.ID, initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync:       1, // Full
			CompletionProvider:     &completionOptions{},
			DefinitionProvider:     true,
			DocumentSymbolProvider: true,
			ExecuteCommandProvider: &executeCommandOptions{Commands: []string{"sqlgen.generatePreview"}},
		},
		ServerInfo: serverInfo{Name: "sqlgen", Version: s.version},
	})
}

func (s *server) handleExecuteCommand(req request, p executeCommandParams) error {
	if p.Command != "sqlgen.generatePreview" {
		return s.respond(req.ID, nil)
	}
	// First argument is the document URI.
	var uri string
	if len(p.Arguments) > 0 {
		_ = json.Unmarshal(p.Arguments[0], &uri)
	}
	text := s.docs[uri]
	preview, err := generatePreview(uri, text)
	if err != nil {
		return s.respond(req.ID, map[string]string{"error": err.Error()})
	}
	return s.respond(req.ID, map[string]string{"preview": preview})
}

func (s *server) publishDiagnostics(uri string) {
	diags := computeDiagnostics(uri, s.docs[uri])
	_ = s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}
