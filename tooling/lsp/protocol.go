// Package lsp is `fiig lsp`, the language server ADR 0089 decides: the
// Language Server Protocol 3.17 over standard input and output, answering
// from the compiler's own checker so an editor shows what `fiig check`
// shows.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

// ADR 0089 §1: JSON-RPC 2.0 framed by Content-Length headers, and the
// protocol types the server uses, written here rather than taken from a
// library.

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// conn reads and writes framed messages; writes are serialized, since
// diagnostics are published from the analysis goroutine.
type conn struct {
	r  *bufio.Reader
	mu sync.Mutex
	w  io.Writer
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{r: bufio.NewReaderSize(r, 64*1024), w: w}
}

func (c *conn) read() (*message, error) {
	tp := textproto.NewReader(c.r)
	header, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(header.Get("Content-Length")))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad Content-Length %q", header.Get("Content-Length"))
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return &message{Error: &rpcError{Code: codeParseError, Message: err.Error()}}, nil
	}
	return &m, nil
}

func (c *conn) write(m *message) error {
	m.JSONRPC = "2.0"
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

func (c *conn) notify(method string, params any) error {
	raw, _ := json.Marshal(params)
	return c.write(&message{Method: method, Params: raw})
}

// nullResult is a response whose result is JSON null, which Result's
// omitempty would otherwise drop.
type nullResult struct{}

func (nullResult) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// --- protocol types ---

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type lspDiagnostic struct {
	Range           lspRange         `json:"range"`
	Severity        int              `json:"severity"`
	Code            string           `json:"code,omitempty"`
	CodeDescription *codeDescription `json:"codeDescription,omitempty"`
	Source          string           `json:"source"`
	Message         string           `json:"message"`
}

type codeDescription struct {
	Href string `json:"href"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hover struct {
	Contents markupContent `json:"contents"`
	Range    *lspRange     `json:"range,omitempty"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

type symbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// Symbol kinds (LSP §3.17 SymbolKind).
const (
	symModule      = 2
	symMethod      = 6
	symField       = 8
	symEnum        = 10
	symInterface   = 11
	symFunction    = 12
	symConstant    = 14
	symEnumMember  = 22
	symStruct      = 23
	diagnosticKind = 1 // DiagnosticSeverity.Error, the only one (ADR 0051)
)
