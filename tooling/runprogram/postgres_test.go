package runprogram

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
)

// ADR 0085: fiig.postgres, the driver built into the toolchain, against
// RFC 7677's SCRAM vector and a fake Postgres server that speaks the wire
// protocol (startup, authentication, the extended query protocol).

var pgProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "pgtest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.postgres": "1.0.0" },
    "capabilities": ["Database", "Socket"],
    "nativeCapabilities": ["Database"]
}`,
	"pet.fiig": `model Pet {
    name: string
    weight: decimal
    born: string | None
}`,
	"find_pet.fiig": `gateway findPet(name: string) -> Pet | None
    requires Database
    idempotent
{
    return Database.queryOne("SELECT name, weight, born FROM pets WHERE name = ?", name)
}`,
}

// pgLoad writes pgProgram to a fresh root and loads it with no workspace,
// so fiig.postgres can only come from the toolchain.
func pgLoad(t *testing.T) (types.Packages, *types.Universe, map[string]map[string]*types.Package, map[string]map[string]*types.GroupRef) {
	t.Helper()
	root := t.TempDir()
	for name, src := range pgProgram {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	return loaded.Packages, u, BuildImports(files, loaded.Packages), BuildGroups(files, loaded.Packages)
}

// pgInterp is an interpreter whose Database is fiig.postgres with settings.
func pgInterp(t *testing.T, settings map[string]any) (*runtime.Interp, *runtime.DriverProvider) {
	t.Helper()
	packages, u, imports, groups := pgLoad(t)
	caps, err := runtime.LoadCapabilities(map[string]map[string]any{
		"Database": {"type": "driver", "driver": "fiig.postgres", "settings": settings},
	}, runtime.FactoryRegistry{"driver": runtime.DriverFactory})
	if err != nil {
		t.Fatal(err)
	}
	caps.Register("Socket", runtime.NewSocketProvider())
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports, in.Groups, in.Packages = imports, groups, packages
	t.Cleanup(caps.Close)
	p, _ := caps.Lookup("Database")
	return in, p.(*runtime.DriverProvider)
}

// pgCall calls one of fiig.postgres's own declarations.
func pgCall(t *testing.T, name string, args map[string]runtime.Value) runtime.Value {
	t.Helper()
	in, _ := pgInterp(t, map[string]any{"host": "unused", "user": "u"})
	ctx := in.EnterPackage(context.Background(), in.Packages["fiig.postgres"])
	v, f := in.Call(ctx, name, args)
	if f != nil {
		t.Fatalf("%s: %v", name, f)
	}
	return v
}

func TestScramMatchesRFC7677(t *testing.T) {
	serverFirst := "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	v := pgCall(t, "scramFinal", map[string]runtime.Value{
		"password":        runtime.Bytes{B: []byte("pencil")},
		"clientFirstBare": runtime.Str("n=user,r=rOprNGfwEbeRWgbNEkqO"),
		"clientNonce":     runtime.Str("rOprNGfwEbeRWgbNEkqO"),
		"serverFirst":     runtime.Str(serverFirst),
	})
	r := v.(runtime.Result)
	if !r.Ok {
		t.Fatalf("scramFinal = %v", v)
	}
	final := r.Value.(runtime.Record)
	if got := string(final.Fields["message"].(runtime.Str)); got != "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=" {
		t.Errorf("client-final = %s", got)
	}
	if got := base64.StdEncoding.EncodeToString(final.Fields["serverSignature"].(runtime.Bytes).B); got != "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Errorf("server signature = %s", got)
	}
}

func TestScramRefusesAHostileServerFirst(t *testing.T) {
	for _, first := range []string{
		"r=someoneElse,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"r=abcEXTRA,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=5000000",
		"r=abcEXTRA,i=4096",
	} {
		v := pgCall(t, "scramFinal", map[string]runtime.Value{
			"password": runtime.Bytes{B: []byte("pw")}, "clientFirstBare": runtime.Str("n=,r=abc"),
			"clientNonce": runtime.Str("abc"), "serverFirst": runtime.Str(first),
		})
		if v.(runtime.Result).Ok {
			t.Errorf("scramFinal accepted %q", first)
		}
	}
}

func TestPlaceholdersAreNumbered(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM t WHERE a = ? AND b = ?":      "SELECT * FROM t WHERE a = $1 AND b = $2",
		"SELECT '?' , ? FROM t":                      "SELECT '?' , $1 FROM t",
		"SELECT 'it''s ?', ?":                        "SELECT 'it''s ?', $1",
		`SELECT E'a\'?', ?`:                          `SELECT E'a\'?', $1`,
		`SELECT "col?" FROM t WHERE x = ?`:           `SELECT "col?" FROM t WHERE x = $1`,
		"SELECT ? -- what?\n, ?":                     "SELECT $1 -- what?\n, $2",
		"SELECT /* a /* nested ? */ ? */ ?":          "SELECT /* a /* nested ? */ ? */ $1",
		"SELECT $tag$ ? $tag$, ?":                    "SELECT $tag$ ? $tag$, $1",
		"SELECT $$?$$, ?":                            "SELECT $$?$$, $1",
		"SELECT doc ?? 'key', doc ??| array['a'], ?": "SELECT doc ? 'key', doc ?| array['a'], $1",
		"SELECT $1, $2":                              "SELECT $1, $2",
		"SELECT 'é', ?":                              "SELECT 'é', $1",
		"SELECT 'unterminated ?":                     "SELECT 'unterminated ?",
	}
	for in, want := range cases {
		got := pgCall(t, "numberPlaceholders", map[string]runtime.Value{"sql": runtime.Str(in)})
		if string(got.(runtime.Str)) != want {
			t.Errorf("numberPlaceholders(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestColumnTextIsReadByType(t *testing.T) {
	cases := []struct {
		text    string
		oid     int64
		variant string
		value   runtime.Value
	}{
		{"42", 23, "Int", runtime.Int(42)},
		{"-7", 20, "Int", runtime.Int(-7)},
		{"2.5", 701, "Float", runtime.Float(2.5)},
		{"NaN", 701, "Text", runtime.Str("NaN")},
		{"-Infinity", 700, "Text", runtime.Str("-Infinity")},
		{"t", 16, "Bool", runtime.Bool(true)},
		{`\x00ff`, 17, "Bytes", runtime.Bytes{B: []byte{0, 255}}},
		{"19.90", 1700, "Text", runtime.Str("19.90")},
		{"2026-11-17", 1082, "Text", runtime.Str("2026-11-17")},
	}
	for _, tc := range cases {
		v := pgCall(t, "columnValue", map[string]runtime.Value{"text": runtime.Bytes{B: []byte(tc.text)}, "oid": runtime.Int(tc.oid)})
		e := v.(runtime.EnumValue)
		if e.Variant != tc.variant || !runtime.Equal(e.Fields[0], tc.value) {
			t.Errorf("columnValue(%q, %d) = %v, want %s(%v)", tc.text, tc.oid, v, tc.variant, tc.value)
		}
	}
}

// --- a fake Postgres server ---

type pgAuth string

const (
	authTrust     pgAuth = "trust"
	authScram     pgAuth = "scram"
	authScramLies pgAuth = "scram-lies" // sends a wrong server signature
	authMD5       pgAuth = "md5"
	authCleartext pgAuth = "cleartext"
)

type pgParam struct {
	format int
	value  []byte // nil for NULL
}

type pgReply struct {
	columns       []string
	oids          []int32
	rows          [][]*string
	tag           string
	errCode       string
	errMsg        string
	errConstraint string
}

type fakePostgres struct {
	t        *testing.T
	auth     pgAuth
	password string
	handle   func(sql string, params []pgParam) pgReply

	mu       sync.Mutex
	accepted int
	startup  map[string]string
	queries  []string
}

func (s *fakePostgres) listen(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.accepted++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func msg(kind byte, body []byte) []byte {
	out := []byte{kind}
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)+4))
	return append(out, body...)
}

func auth(code uint32, data []byte) []byte {
	return msg('R', append(binary.BigEndian.AppendUint32(nil, code), data...))
}

func cstr(s string) []byte { return append([]byte(s), 0) }

func errorResponse(code, message string) []byte {
	body := append([]byte{'S'}, cstr("ERROR")...)
	body = append(body, 'C')
	body = append(body, cstr(code)...)
	body = append(body, 'M')
	body = append(body, cstr(message)...)
	return msg('E', append(body, 0))
}

// errorResponseNamed is errorResponse with the constraint's name, field n.
func errorResponseNamed(code, message, constraint string) []byte {
	if constraint == "" {
		return errorResponse(code, message)
	}
	e := errorResponse(code, message)
	body := append(e[5:len(e)-1], 'n')
	body = append(body, cstr(constraint)...)
	return msg('E', append(body, 0))
}

func readMsg(r io.Reader) (byte, []byte, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(head[1:])-4)
	_, err := io.ReadFull(r, body)
	return head[0], body, err
}

func (s *fakePostgres) serve(c net.Conn) {
	defer c.Close()
	var size uint32
	if binary.Read(c, binary.BigEndian, &size) != nil {
		return
	}
	body := make([]byte, size-4)
	if _, err := io.ReadFull(c, body); err != nil {
		return
	}
	if binary.BigEndian.Uint32(body) == 80877103 {
		c.Write([]byte{'N'}) // no TLS here
		return
	}
	if binary.BigEndian.Uint32(body) != 196608 {
		s.t.Errorf("startup protocol = %d", binary.BigEndian.Uint32(body))
		return
	}
	params := map[string]string{}
	parts := bytes.Split(body[4:], []byte{0})
	for i := 0; i+1 < len(parts) && len(parts[i]) > 0; i += 2 {
		params[string(parts[i])] = string(parts[i+1])
	}
	s.mu.Lock()
	s.startup = params
	s.mu.Unlock()
	if !s.authenticate(c) {
		return
	}
	c.Write(msg('S', append(cstr("server_version"), cstr("16.0")...)))
	c.Write(msg('K', make([]byte, 8)))
	c.Write(msg('Z', []byte{'I'}))
	s.queryLoop(c)
}

func (s *fakePostgres) authenticate(c net.Conn) bool {
	switch s.auth {
	case authTrust:
		c.Write(auth(0, nil))
		return true
	case authMD5:
		c.Write(auth(5, []byte{1, 2, 3, 4}))
		readMsg(c)
		return false
	case authCleartext:
		c.Write(auth(3, nil))
		readMsg(c)
		return false
	}
	c.Write(auth(10, append(cstr("SCRAM-SHA-256"), 0)))
	_, first, err := readMsg(c)
	if err != nil {
		return false
	}
	mech := first[:bytes.IndexByte(first, 0)]
	if string(mech) != "SCRAM-SHA-256" {
		s.t.Errorf("mechanism = %s", mech)
	}
	clientFirst := string(first[len(mech)+5:])
	bare := strings.TrimPrefix(clientFirst, "n,,")
	nonce := strings.TrimPrefix(bare, "n=,r=") + "server"
	salt := []byte("fiig-salt")
	serverFirst := "r=" + nonce + ",s=" + base64.StdEncoding.EncodeToString(salt) + ",i=4096"
	c.Write(auth(11, []byte(serverFirst)))
	_, final, err := readMsg(c)
	if err != nil {
		return false
	}
	withoutProof, proofText, _ := strings.Cut(string(final), ",p=")
	authMessage := bare + "," + serverFirst + "," + withoutProof
	salted, _ := pbkdf2.Key(sha256.New, s.password, salt, 4096, 32)
	mac := func(key []byte, data string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(data))
		return h.Sum(nil)
	}
	clientKey := mac(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	signature := mac(stored[:], authMessage)
	proof, _ := base64.StdEncoding.DecodeString(proofText)
	for i := range proof {
		proof[i] ^= signature[i]
	}
	if !bytes.Equal(proof, clientKey) {
		c.Write(errorResponse("28P01", `password authentication failed for user "app"`))
		return false
	}
	serverSignature := mac(mac(salted, "Server Key"), authMessage)
	if s.auth == authScramLies {
		serverSignature[0] ^= 1
	}
	c.Write(auth(12, []byte("v="+base64.StdEncoding.EncodeToString(serverSignature))))
	c.Write(auth(0, nil))
	return true
}

func (s *fakePostgres) queryLoop(c net.Conn) {
	var sql string
	var params []pgParam
	for {
		kind, body, err := readMsg(c)
		if err != nil || kind == 'X' {
			return
		}
		switch kind {
		case 'P':
			rest := body[bytes.IndexByte(body, 0)+1:]
			sql = string(rest[:bytes.IndexByte(rest, 0)])
		case 'B':
			params = parseBind(body)
		case 'S':
			s.mu.Lock()
			s.queries = append(s.queries, sql)
			s.mu.Unlock()
			c.Write(s.respond(sql, params))
		}
	}
}

func parseBind(body []byte) []pgParam {
	r := body[bytes.IndexByte(body, 0)+1:]
	r = r[bytes.IndexByte(r, 0)+1:]
	nf := int(binary.BigEndian.Uint16(r))
	r = r[2:]
	formats := make([]int, nf)
	for i := range formats {
		formats[i] = int(binary.BigEndian.Uint16(r))
		r = r[2:]
	}
	np := int(binary.BigEndian.Uint16(r))
	r = r[2:]
	params := make([]pgParam, np)
	for i := range params {
		n := int32(binary.BigEndian.Uint32(r))
		r = r[4:]
		params[i].format = formats[i]
		if n >= 0 {
			params[i].value = r[:n]
			r = r[n:]
		}
	}
	return params
}

func (s *fakePostgres) respond(sql string, params []pgParam) []byte {
	reply := s.handle(sql, params)
	var out []byte
	out = append(out, msg('1', nil)...)
	out = append(out, msg('N', append(append([]byte{'S'}, cstr("NOTICE")...), 0))...)
	if reply.errCode != "" {
		out = append(out, errorResponseNamed(reply.errCode, reply.errMsg, reply.errConstraint)...)
		return append(out, msg('Z', []byte{'I'})...)
	}
	out = append(out, msg('2', nil)...)
	if reply.columns == nil {
		out = append(out, msg('n', nil)...)
	} else {
		desc := binary.BigEndian.AppendUint16(nil, uint16(len(reply.columns)))
		for i, name := range reply.columns {
			desc = append(desc, cstr(name)...)
			desc = append(desc, make([]byte, 6)...)
			desc = binary.BigEndian.AppendUint32(desc, uint32(reply.oids[i]))
			desc = append(desc, make([]byte, 8)...)
		}
		out = append(out, msg('T', desc)...)
		for _, row := range reply.rows {
			d := binary.BigEndian.AppendUint16(nil, uint16(len(row)))
			for _, v := range row {
				if v == nil {
					d = binary.BigEndian.AppendUint32(d, 0xFFFFFFFF)
					continue
				}
				d = binary.BigEndian.AppendUint32(d, uint32(len(*v)))
				d = append(d, *v...)
			}
			out = append(out, msg('D', d)...)
		}
	}
	out = append(out, msg('C', cstr(reply.tag))...)
	return append(out, msg('Z', []byte{'I'})...)
}

func text(s string) *string { return &s }

func pgSettings(port, password string) map[string]any {
	return map[string]any{"host": "127.0.0.1", "port": port, "user": "app", "password": password, "database": "zoo", "sslmode": "disable", "applicationName": "fiig-test"}
}

// pets answers the queries the tests send, as Postgres would.
func pets(sql string, params []pgParam) pgReply {
	switch {
	case strings.HasPrefix(sql, "SELECT name, weight, born FROM pets WHERE name = $1"):
		if string(params[0].value) != "Rex" {
			return pgReply{columns: []string{"name", "weight", "born"}, oids: []int32{25, 1700, 1082}, tag: "SELECT 0"}
		}
		return pgReply{columns: []string{"name", "weight", "born"}, oids: []int32{25, 1700, 1082},
			rows: [][]*string{{text("Rex"), text("12.50"), nil}}, tag: "SELECT 1"}
	case strings.HasPrefix(sql, "INSERT INTO pets"):
		return pgReply{errCode: "23505", errMsg: `duplicate key value violates unique constraint "pets_pkey"`, errConstraint: "pets_pkey"}
	case strings.HasPrefix(sql, "INSERT"):
		return pgReply{tag: "INSERT 0 " + strconv.Itoa(len(params)/2)}
	case strings.HasPrefix(sql, "BAD"):
		return pgReply{errCode: "42601", errMsg: `syntax error at or near "BAD"`}
	case sql == "BEGIN" || sql == "COMMIT" || sql == "ROLLBACK":
		return pgReply{tag: sql}
	}
	return pgReply{errCode: "42P01", errMsg: "unexpected query " + sql}
}

func TestPostgresConnectsWithScramAndReadsTypedRows(t *testing.T) {
	server := &fakePostgres{t: t, auth: authScram, password: "s3cret", handle: pets}
	in, pool := pgInterp(t, pgSettings(server.listen(t), "s3cret"))
	v, f := in.Call(context.Background(), "findPet", map[string]runtime.Value{"name": runtime.Str("Rex")})
	if f != nil {
		t.Fatal(f)
	}
	rec := v.(runtime.Record)
	if rec.TypeName != "Pet" || rec.Fields["weight"].(runtime.Decimal).String() != "12.50" {
		t.Fatalf("findPet = %v, want Pet with weight 12.50", v)
	}
	if _, isNone := rec.Fields["born"].(runtime.None); !isNone {
		t.Errorf("born = %v, want None from NULL", rec.Fields["born"])
	}
	if v, _ := in.Call(context.Background(), "findPet", map[string]runtime.Value{"name": runtime.Str("Tom")}); v != runtime.Value(runtime.None{}) {
		t.Errorf("findPet(Tom) = %v, want None", v)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.accepted != 1 || pool.Idle() != 1 {
		t.Errorf("accepted %d, idle %d; want one pooled connection", server.accepted, pool.Idle())
	}
	if server.startup["user"] != "app" || server.startup["database"] != "zoo" || server.startup["client_encoding"] != "UTF8" || server.startup["DateStyle"] != "ISO" || server.startup["application_name"] != "fiig-test" {
		t.Errorf("startup = %v", server.startup)
	}
}

func TestPostgresBindsEveryValueKind(t *testing.T) {
	var got []pgParam
	server := &fakePostgres{t: t, auth: authTrust, handle: func(sql string, params []pgParam) pgReply {
		got = params
		return pgReply{tag: "INSERT 0 1"}
	}}
	in, _ := pgInterp(t, pgSettings(server.listen(t), ""))
	p, _ := in.Capabilities.Lookup("Database")
	n, f := p.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO t VALUES (?, ?, ?, ?, ?, ?)"),
		runtime.Int(-3), runtime.Float(1.5), runtime.Str("hi"), runtime.Bool(true), runtime.None{}, runtime.Bytes{B: []byte{0, 1}},
	})
	if f != nil || n != runtime.Value(runtime.Int(1)) {
		t.Fatalf("execute = %v, %v", n, f)
	}
	want := []pgParam{{0, []byte("-3")}, {0, []byte("1.5")}, {0, []byte("hi")}, {0, []byte("true")}, {0, nil}, {1, []byte{0, 1}}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("params = %v, want %v", got, want)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.queries[0] != "INSERT INTO t VALUES ($1, $2, $3, $4, $5, $6)" {
		t.Errorf("sql = %q", server.queries[0])
	}
}

func TestPostgresServerErrorKeepsTheConnection(t *testing.T) {
	server := &fakePostgres{t: t, auth: authTrust, handle: pets}
	in, pool := pgInterp(t, pgSettings(server.listen(t), ""))
	p, _ := in.Capabilities.Lookup("Database")
	_, f := p.Call(context.Background(), "execute", []runtime.Value{runtime.Str("BAD")})
	if f == nil || !strings.Contains(f.Error(), `42601 syntax error at or near "BAD"`) {
		t.Fatalf("fault = %v, want the SQLSTATE and message", f)
	}
	if _, f := p.Call(context.Background(), "execute", []runtime.Value{runtime.Str("INSERT INTO t VALUES (?, ?)"), runtime.Int(1), runtime.Int(2)}); f != nil {
		t.Fatal(f)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.accepted != 1 || pool.Idle() != 1 {
		t.Errorf("accepted %d, idle %d; a server error should keep the connection", server.accepted, pool.Idle())
	}
}

func TestPostgresTransactionSendsBeginAndCommit(t *testing.T) {
	server := &fakePostgres{t: t, auth: authTrust, handle: pets}
	_, pool := pgInterp(t, pgSettings(server.listen(t), ""))
	tx, f := pool.Begin(context.Background(), "")
	if f != nil {
		t.Fatal(f)
	}
	if _, f := tx.Call(context.Background(), "execute", []runtime.Value{runtime.Str("INSERT INTO t VALUES (?, ?)"), runtime.Int(1), runtime.Int(2)}); f != nil {
		t.Fatal(f)
	}
	if f := tx.Commit(); f != nil {
		t.Fatal(f)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if got := strings.Join(server.queries, "; "); got != "BEGIN; INSERT INTO t VALUES ($1, $2); COMMIT" {
		t.Errorf("queries = %s", got)
	}
}

func TestPostgresRefusals(t *testing.T) {
	cases := []struct {
		auth     pgAuth
		password string
		settings map[string]any
		fragment string
	}{
		{authScram, "wrong", nil, `28P01 password authentication failed`},
		{authScramLies, "s3cret", nil, "SCRAM signature is wrong"},
		{authMD5, "s3cret", nil, "MD5"},
		{authCleartext, "s3cret", nil, "only over TLS"},
		{authTrust, "", map[string]any{"sslmode": "verify-full"}, "doesn't offer TLS"},
		{authTrust, "", map[string]any{"sslmode": "require"}, "verify-full or disable"},
		{authTrust, "", map[string]any{"port": "fifty"}, "port setting must be a number"},
	}
	for _, tc := range cases {
		server := &fakePostgres{t: t, auth: tc.auth, password: "s3cret", handle: pets}
		settings := pgSettings(server.listen(t), tc.password)
		for k, v := range tc.settings {
			settings[k] = v
		}
		in, _ := pgInterp(t, settings)
		_, f := in.Call(context.Background(), "findPet", map[string]runtime.Value{"name": runtime.Str("Rex")})
		if f == nil || !strings.Contains(f.Error(), tc.fragment) {
			t.Errorf("%s %v: fault = %v, want %q", tc.auth, tc.settings, f, tc.fragment)
		}
	}
}

// ADR 0087 §4: fiig.postgres reads the constraint's name from the
// ErrorResponse, so the conflict names it.
func TestPostgresReportsTheConstraint(t *testing.T) {
	server := &fakePostgres{t: t, auth: authTrust, handle: pets}
	in, pool := pgInterp(t, pgSettings(server.listen(t), ""))
	_, f := in.Capabilities.Call(context.Background(), "Database", "execute", []runtime.Value{runtime.Str("INSERT INTO pets VALUES (?)"), runtime.Str("Rex")})
	if f == nil || f.Conflict == nil {
		t.Fatalf("fault = %v, want one carrying a conflict", f)
	}
	if f.Conflict.Variant != "Unique" || f.Conflict.Name != "pets_pkey" {
		t.Fatalf("conflict = %+v, want Unique(pets_pkey)", f.Conflict)
	}
	if pool.Idle() != 1 {
		t.Errorf("idle = %d; a conflict leaves the connection usable", pool.Idle())
	}
}
