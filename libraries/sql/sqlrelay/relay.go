// Package sqlrelay is the server side of examples/sql-relay, a database
// driver written in fiig (ADR 0084). It exists to test the driver
// contract without a Postgres server: a client sends one statement per
// frame, a 4-byte big-endian length and that many bytes of JSON,
//
//	{"sql": "...", "values": [{"variant": "Int", "value": 5}, ...]}
//
// and gets back one frame holding a Result<SqlResult, DatabaseError> in
// ADR 0072's JSON shape. Each TCP connection gets its own database
// session (*sql.Conn), so BEGIN and COMMIT sent on it reach the same
// session, as they would on a Postgres connection.
package sqlrelay

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"fiig-lang/compiler/runtime"
)

// Server relays statements to db.
type Server struct {
	DB *sql.DB
	// Accepted counts the connections the server has accepted, so a test
	// can see the pool reusing one.
	Accepted atomic.Int64

	mu    sync.Mutex
	conns map[net.Conn]bool
}

// DropAll closes every client connection, as a server restart would, so
// a test can see the pool discard a broken one.
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.Accepted.Add(1)
		s.mu.Lock()
		if s.conns == nil {
			s.conns = map[net.Conn]bool{}
		}
		s.conns[c] = true
		s.mu.Unlock()
		go s.handle(c)
	}
}

type request struct {
	SQL    string            `json:"sql"`
	Values []json.RawMessage `json:"values"`
}

type sqlValue struct {
	Variant string          `json:"variant"`
	Value   json.RawMessage `json:"value"`
}

func (s *Server) handle(c net.Conn) {
	defer func() {
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	ctx := context.Background()
	session, err := s.DB.Conn(ctx)
	if err != nil {
		return
	}
	defer session.Close()
	// A client that goes away mid-transaction leaves it open on the session,
	// which database/sql would hand to the next client; Postgres rolls back
	// when a connection drops, and so does the relay.
	defer session.ExecContext(ctx, "ROLLBACK")
	for {
		var size uint32
		if err := binary.Read(c, binary.BigEndian, &size); err != nil {
			return
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(c, body); err != nil {
			return
		}
		reply := s.answer(ctx, session, body)
		out, _ := json.Marshal(reply)
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(out)))
		if _, err := c.Write(append(frame, out...)); err != nil {
			return
		}
	}
}

func ok(v any) map[string]any { return map[string]any{"variant": "Ok", "value": v} }

func failure(code, message string) map[string]any {
	return map[string]any{"variant": "Err", "error": map[string]any{"variant": "Server", "code": code, "message": message}}
}

// dbFailure reports a database error as Postgres would: a SQLSTATE, and
// for a conflict the name ADR 0087 §2 gives it on SQLite, the column list,
// as the constraint (or, for NOT NULL, the column).
func dbFailure(err error) map[string]any {
	reply := failure(code(err), err.Error())
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return reply
	}
	c := runtime.SQLiteConflict(coded.Code(), err.Error())
	if c == nil {
		return reply
	}
	state := map[string]string{"Unique": "23505", "ForeignKey": "23503", "NotNull": "23502", "Check": "23514", "Retryable": "40001"}[c.Variant]
	e := map[string]any{"variant": "Server", "code": state, "message": err.Error()}
	if c.Variant == "NotNull" {
		e["column"] = c.Name
	} else {
		e["constraint"] = c.Name
	}
	return map[string]any{"variant": "Err", "error": e}
}

func (s *Server) answer(ctx context.Context, session *sql.Conn, body []byte) map[string]any {
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return failure("08P01", "malformed request: "+err.Error())
	}
	args := make([]any, len(req.Values))
	for i, raw := range req.Values {
		v, err := bind(raw)
		if err != nil {
			return failure("08P01", fmt.Sprintf("value %d: %v", i+1, err))
		}
		args[i] = v
	}
	if !returnsRows(req.SQL) {
		res, err := session.ExecContext(ctx, req.SQL, args...)
		if err != nil {
			return dbFailure(err)
		}
		n, _ := res.RowsAffected()
		return ok(map[string]any{"columns": []string{}, "rows": [][]any{}, "affected": n})
	}
	rows, err := session.QueryContext(ctx, req.SQL, args...)
	if err != nil {
		return dbFailure(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := [][]any{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return dbFailure(err)
		}
		row := make([]any, len(cols))
		for i, v := range raw {
			row[i] = encode(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return dbFailure(err)
	}
	return ok(map[string]any{"columns": cols, "rows": out, "affected": 0})
}

// returnsRows is a dev relay's guess at which statements read: enough for
// the examples, not a SQL parser.
func returnsRows(text string) bool {
	head := strings.ToUpper(strings.TrimSpace(text))
	for _, p := range []string{"SELECT", "WITH", "VALUES", "PRAGMA"} {
		if strings.HasPrefix(head, p) {
			return true
		}
	}
	return false
}

func bind(raw json.RawMessage) (any, error) {
	var v sqlValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	switch v.Variant {
	case "Null":
		return nil, nil
	case "Int":
		var n int64
		err := json.Unmarshal(v.Value, &n)
		return n, err
	case "Float":
		var f float64
		err := json.Unmarshal(v.Value, &f)
		return f, err
	case "Text":
		var s string
		err := json.Unmarshal(v.Value, &s)
		return s, err
	case "Bool":
		var b bool
		err := json.Unmarshal(v.Value, &b)
		return b, err
	case "Bytes":
		var s string
		if err := json.Unmarshal(v.Value, &s); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(s)
	}
	return nil, fmt.Errorf("unknown SqlValue variant %q", v.Variant)
}

func encode(v any) map[string]any {
	switch v := v.(type) {
	case nil:
		return map[string]any{"variant": "Null"}
	case int64:
		return map[string]any{"variant": "Int", "value": v}
	case float64:
		return map[string]any{"variant": "Float", "value": v}
	case bool:
		return map[string]any{"variant": "Bool", "value": v}
	case string:
		return map[string]any{"variant": "Text", "value": v}
	case []byte:
		return map[string]any{"variant": "Bytes", "value": base64.StdEncoding.EncodeToString(v)}
	}
	return map[string]any{"variant": "Text", "value": fmt.Sprint(v)}
}

// code is the error's SQLite result code when the driver gives one, as a
// stand-in for Postgres's SQLSTATE.
func code(err error) string {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return "sqlite" + strconv.Itoa(coded.Code())
	}
	return "XX000"
}
