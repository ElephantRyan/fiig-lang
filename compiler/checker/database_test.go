package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0082: Database's calls are typed, and every capability with no
// declaration in fiig is held to its gateway's requires.

// twinsModel's two fields would read one column (ADR 0094 §3).
const twinsModel = `model Twins {
    userId: string;
    user_id: string;
}`

const shipmentModel = `model Shipment {
    id: string;
    weight: decimal;
    note: string | None;
    state: ShipState;
    fragile: bool;
    label: bytes;
}`

const shipStateEnum = `enum ShipState {
    Packed,
    Sent from json "sent",
}`

func gatewayWith(body string) string {
	return `gateway g(id: string) -> int
    requires Database
{
` + body + `
}`
}

func TestDatabaseRowsTakeTheirTypeFromContext(t *testing.T) {
	clean := []string{
		`gateway g(id: string) -> Shipment | None
    requires Database
{
    return Database.queryOne("SELECT id, weight, note, state, fragile, label FROM shipments WHERE id = ?", id);
}`,
		`gateway g() -> List<Shipment>
    requires Database
{
    return Database.queryMany("SELECT id, weight, note, state, fragile, label FROM shipments");
}`,
		gatewayWith(`    row: Shipment | None = Database.queryOne("SELECT id, weight, note, state, fragile, label FROM shipments WHERE id = ?", id);
    return match (row) {
        None -> {
            return 0;
        }
        found -> {
            return found.id.length();
        }
    }`),
		gatewayWith(`    count: int | None = Database.queryOne("SELECT COUNT(*) FROM shipments");
    ids: List<string> = Database.queryMany("SELECT id FROM shipments");
    notes: List<string | None> = Database.queryMany("SELECT note FROM shipments");
    return ids.length();`),
	}
	for _, src := range clean {
		if diags := checkSrc(t, src, shipmentModel, shipStateEnum); len(diags) != 0 {
			t.Errorf("diags = %v, want none for\n%s", diags, src)
		}
	}
}

func TestDatabaseQueryRecordsItsRowType(t *testing.T) {
	f := mustParse(t, gatewayWith(`    ids: List<string> = Database.queryMany("SELECT id FROM shipments");
    return ids.length();`))
	if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	g := f.Decls[0].(*syntax.GatewayDecl)
	assign := g.Body.Stmts[0].(*syntax.Binding)
	call := assign.Value.(*syntax.CallExpr)
	if got, ok := call.DecodeInto.(types.Type); !ok || got.String() != "string" {
		t.Fatalf("DecodeInto = %v, want string", call.DecodeInto)
	}
}

func TestDatabaseMisuse(t *testing.T) {
	cases := []struct {
		body, rule, fragment string
	}{
		{`    row = Database.queryOne("SELECT id FROM shipments");
    return 0;`, "A12", "Database.queryOne has no row type"},
		{`    row: Shipment = Database.queryOne("SELECT id FROM shipments");
    return 0;`, "A1", "read it as `Shipment | None`"},
		{`    rows: Shipment = Database.queryMany("SELECT id FROM shipments");
    return 0;`, "A1", "returns a List of rows"},
		{`    row: Shipment | string | None = Database.queryOne("SELECT id FROM shipments");
    return 0;`, "Q3", "reads one row type"},
		{`    rows: List<List<int>> = Database.queryMany("SELECT id FROM shipments");
    return 0;`, "Q3", "it is List<int>"},
		{`    sql = "DELETE FROM shipments";
    return Database.execute(sql);`, "Q1", "must be a string literal"},
		{`    return Database.execute("DELETE FROM shipments WHERE id = ${id}")`, "Q1", "no ${...}"},
		{`    return Database.execute("DELETE FROM shipments WHERE id = ?", [id])`, "Q2", "List<string>"},
		{`    return Database.execute("DELETE FROM shipments WHERE id = ?", id: id)`, "", "positional"},
		{`    return Database.delete("shipments")`, "K1", `Database has no operation "delete"`},
		{`    return EmailSender.send(id)`, "P7", "may not use EmailSender"},
		{`    row: Twins | None = Database.queryOne("SELECT user_id FROM twins");
    return 0;`, "Q3", "fields userId and user_id would read the same column"},
	}
	for _, tc := range cases {
		diags := checkSrc(t, gatewayWith(tc.body), shipmentModel, shipStateEnum, twinsModel)
		if len(diags) != 1 || diags[0].Rule != tc.rule || !strings.Contains(diags[0].Msg, tc.fragment) {
			t.Errorf("%s\ndiags = %v, want one %q containing %q", tc.body, diags, tc.rule, tc.fragment)
		}
	}
}

func TestDatabaseBindsScalarsEnumsAndNone(t *testing.T) {
	src := `gateway g(s: Shipment, maybe: string | None, key: Secret) -> int
    requires Database
{
    return Database.execute("INSERT INTO shipments VALUES (?, ?, ?, ?, ?, ?, ?, ?)", s.id, s.weight, s.note, s.state, s.fragile, s.label, maybe, None);
}`
	if diags := checkSrc(t, src, shipmentModel, shipStateEnum); len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	secret := `gateway g(key: Secret) -> int
    requires Database
{
    return Database.execute("INSERT INTO keys VALUES (?)", key);
}`
	wantDiag(t, checkSrc(t, secret), "Q2", "Secret")
}

// §2: every kind but a gateway is B10 on Database and on any capability
// with no declaration in fiig; a name a declaration takes is not one.
func TestNativeCapabilitiesOutsideGatewaysAreB10(t *testing.T) {
	for _, kind := range []string{"rule", "activity", "conversion"} {
		src := kind + ` k(id: string) -> int {
    return Database.execute("DELETE FROM t WHERE id = ?", id)
}`
		wantDiag(t, checkSrc(t, src), "B10", "Database.execute")
	}
	wantDiag(t, checkSrc(t, `activity k(to: string) -> None {
    PaymentGateway.charge(to);
    return None;
}`), "B10", "PaymentGateway.charge")
}

// ADR 0087 §1: Result<..., DatabaseConflict> from context opts a call in;
// everything else is unchanged.
func TestDatabaseConflictsOptInByContext(t *testing.T) {
	clean := []string{
		`gateway g(id: string) -> Result<int, DatabaseConflict>
    requires Database
{
    return Database.execute("DELETE FROM t WHERE id = ?", id);
}`,
		gatewayWith(`    inserted: Result<int, DatabaseConflict> = Database.execute("INSERT INTO t VALUES (?)", id);
    return match (inserted) {
        Ok(n) -> {
            return n;
        }
        Err(DatabaseConflict.Unique(constraint)) -> {
            return 0;
        }
        Err(other) -> {
            return -1;
        }
    }`),
		gatewayWith(`    claimed: Result<string | None, DatabaseConflict> = Database.queryOne("INSERT INTO t VALUES (?) RETURNING id", id);
    rows: Result<List<Shipment>, DatabaseConflict> = Database.queryMany("SELECT id, weight, note, state, fragile, label FROM shipments");
    return 0;`),
	}
	for _, src := range clean {
		if diags := checkSrc(t, src, shipmentModel, shipStateEnum); len(diags) != 0 {
			t.Errorf("diags = %v, want none for\n%s", diags, src)
		}
	}
	f := mustParse(t, clean[0])
	if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
		t.Fatal(diags)
	}
	ret := f.Decls[0].(*syntax.GatewayDecl).Body.Stmts[0].(*syntax.ReturnStmt)
	if !ret.Value.(*syntax.CallExpr).HandlesConflicts {
		t.Error("the returned call wasn't recorded as handling conflicts")
	}
	wantDiag(t, checkSrc(t, gatewayWith(`    n: Result<string, DatabaseConflict> = Database.execute("DELETE FROM t");
    return 0;`)), "A1", "expected Result<string, DatabaseConflict>")
	wantDiag(t, checkSrc(t, gatewayWith(`    row: Result<Shipment, DatabaseConflict> = Database.queryOne("SELECT id FROM t");
    return 0;`), shipmentModel, shipStateEnum), "A1", "read it as `Shipment | None`")
}

// ADR 0088 §1, §4: an isolation word and retry on a transaction block.
func TestTransactionRetryAndIsolationCheck(t *testing.T) {
	ok := `activity a() -> int {
    transaction Database serializable retry(attempts: 3) {
        b();
    }
    transaction Database repeatableRead {
        b();
    }
    transaction Database retry(attempts: 2) {
        b();
    }
    return 1;
}`
	gw := `gateway b() -> int
    requires Database
{
    return Database.execute("DELETE FROM t");
}`
	if diags := checkSrc(t, ok, gw); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	wantDiag(t, checkSrc(t, `activity a() -> int {
    transaction Database retry(attempts: 0) {
        b();
    }
    return 1;
}`, gw), "", "at least 1")
}
