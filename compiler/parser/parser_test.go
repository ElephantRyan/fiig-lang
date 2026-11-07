package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

func mustParse(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors for %q: %v", src, errs)
	}
	return f
}

// TestCallExprAndFieldAccessHaveNonZeroSpan guards against a bug where
// parsePostfix built FieldAccess/CallExpr nodes without ever setting their
// Span, leaving it at the zero value — invisible until tooling (the
// formatter) started relying on every expression's Bounds() being accurate
// for comment placement.
func TestCallExprAndFieldAccessHaveNonZeroSpan(t *testing.T) {
	f := mustParse(t, `rule r(x: Thing) -> int {
    return x.y.z(1)
}`)
	body := f.Decls[0].(*syntax.RuleDecl).Body
	ret := body.Stmts[0].(*syntax.ReturnStmt)

	call, ok := ret.Value.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("expected a CallExpr, got %T", ret.Value)
	}
	if call.Start.Line == 0 {
		t.Fatalf("CallExpr has zero-value span: %+v", call.Span)
	}
	if call.Start.Line != 2 {
		t.Fatalf("CallExpr.Start should be at the start of the postfix chain (line 2), got %+v", call.Start)
	}

	fa, ok := call.Callee.(*syntax.FieldAccess)
	if !ok {
		t.Fatalf("expected a FieldAccess, got %T", call.Callee)
	}
	if fa.Start.Line == 0 {
		t.Fatalf("FieldAccess has zero-value span: %+v", fa.Span)
	}
}

// TestValueDeclIsRemoved pins ADR 0055: `value` is no longer a keyword, and
// a file that still starts with it gets a diagnostic naming the ADR rather
// than a generic "expected a declaration".
func TestValueDeclIsRemoved(t *testing.T) {
	_, errs := Parse(`value CustomerId: string`)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for a `value` declaration")
	}
	if !strings.Contains(errs[0].Msg, "ADR 0055") {
		t.Errorf("first error = %q, want one naming ADR 0055", errs[0].Msg)
	}
}

func TestParseModelDecl(t *testing.T) {
	f := mustParse(t, `
model Customer {
    id: CustomerId
    name: string
    email: EmailAddress
}`)
	md := f.Decls[0].(*syntax.ModelDecl)
	if md.Name != "Customer" || len(md.Fields) != 3 {
		t.Fatalf("got %+v", md)
	}
	if md.Fields[0].Name != "id" || md.Fields[2].Name != "email" {
		t.Fatalf("fields = %+v", md.Fields)
	}
}

func TestParseEnumDeclWithZeroFieldVariant(t *testing.T) {
	f := mustParse(t, `
enum CheckoutError {
    Customer(error: CustomerError)
    Order(error: OrderError)
    OutOfStock
}`)
	ed := f.Decls[0].(*syntax.EnumDecl)
	if len(ed.Variants) != 3 {
		t.Fatalf("got %d variants", len(ed.Variants))
	}
	if ed.Variants[2].Name != "OutOfStock" || len(ed.Variants[2].Fields) != 0 {
		t.Fatalf("OutOfStock variant = %+v", ed.Variants[2])
	}
	if len(ed.Variants[0].Fields) != 1 || ed.Variants[0].Fields[0].Name != "error" {
		t.Fatalf("Customer variant = %+v", ed.Variants[0])
	}
}

func TestParseRuleWithTryAndInterpolation(t *testing.T) {
	f := mustParse(t, `
rule greet(name: string) -> string {
    return "Hello, ${name}!"
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ret := rd.Body.Stmts[0].(*syntax.ReturnStmt)
	lit := ret.Value.(*syntax.StringLit)
	if len(lit.Parts) != 3 {
		t.Fatalf("got %d string parts, want 3", len(lit.Parts))
	}
	if lit.Parts[0].Text != "Hello, " {
		t.Errorf("part 0 = %q", lit.Parts[0].Text)
	}
	if id, ok := lit.Parts[1].Interp.(*syntax.Ident); !ok || id.Name != "name" {
		t.Errorf("interpolated part = %#v", lit.Parts[1].Interp)
	}
	if lit.Parts[2].Text != "!" {
		t.Errorf("part 2 = %q", lit.Parts[2].Text)
	}
}

func TestParseGatewayWithRequiresAndIdempotent(t *testing.T) {
	f := mustParse(t, `
gateway getCustomer(id: CustomerId) -> Result<Customer, CustomerError>
    requires Database
    idempotent
{
    row = Database.queryOne("x", id)
    match row {
        found -> Ok(Customer(id: found.id, name: found.name, email: found.email))
        None -> Err(CustomerError.NotFound(id))
    }
}`)
	sd := f.Decls[0].(*syntax.GatewayDecl)
	if sd.Requires != "Database" || !sd.Idempotent {
		t.Fatalf("Requires=%q Idempotent=%v", sd.Requires, sd.Idempotent)
	}
	if len(sd.Body.Stmts) != 2 {
		t.Fatalf("got %d statements, want 2", len(sd.Body.Stmts))
	}
	binding := sd.Body.Stmts[0].(*syntax.Binding)
	if binding.Name != "row" {
		t.Fatalf("binding = %+v", binding)
	}
	match := sd.Body.Stmts[1].(*syntax.MatchStmt)
	if len(match.Arms) != 2 {
		t.Fatalf("got %d match arms", len(match.Arms))
	}
	if _, ok := match.Arms[0].Pattern.(*syntax.NamePattern); !ok {
		t.Errorf("arm 0 pattern = %#v, want NamePattern (found)", match.Arms[0].Pattern)
	}
	nonePattern, ok := match.Arms[1].Pattern.(*syntax.NamePattern)
	if !ok || nonePattern.Path[0] != "None" {
		t.Errorf("arm 1 pattern = %#v, want NamePattern(None)", match.Arms[1].Pattern)
	}
}

func TestParseConcurrentWithLimit(t *testing.T) {
	f := mustParse(t, `
workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {
    concurrent(id in ids, limit: 5):
        concurrent(innerId in ids, limit: 50):
            getCustomer(innerId)
}`)
	wd := f.Decls[0].(*syntax.WorkflowDecl)
	stmt := wd.Body.Stmts[0].(*syntax.ExprStmt)
	outer := stmt.Value.(*syntax.ConcurrentExpr)
	if outer.Var != "id" || outer.Limit == nil || outer.Limit.Unbounded {
		t.Fatalf("outer = %+v", outer)
	}
	outerBody, ok := outer.Body.(syntax.ExprBody)
	if !ok {
		t.Fatalf("outer.Body = %#v, want ExprBody", outer.Body)
	}
	innerBody, ok := outerBody.Expr.(*syntax.ConcurrentExpr)
	if !ok {
		t.Fatalf("outer.Body.Expr = %#v, want nested ConcurrentExpr", outerBody.Expr)
	}
	if innerBody.Var != "innerId" {
		t.Fatalf("inner = %+v", innerBody)
	}
}

func TestParseConcurrentWithPreserveOrder(t *testing.T) {
	f := mustParse(t, `
workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {
    concurrent(id in ids, limit: 5, preserveOrder):
        getCustomer(id)
}`)
	wd := f.Decls[0].(*syntax.WorkflowDecl)
	stmt := wd.Body.Stmts[0].(*syntax.ExprStmt)
	ce := stmt.Value.(*syntax.ConcurrentExpr)
	if !ce.PreserveOrder {
		t.Fatalf("PreserveOrder = false, want true")
	}
	if ce.Limit == nil || ce.Limit.Unbounded {
		t.Fatalf("Limit = %+v, want a literal 5", ce.Limit)
	}

	// Order-independent: `preserveOrder` may come before `limit:` too.
	f2 := mustParse(t, `
workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {
    concurrent(id in ids, preserveOrder, limit: 5):
        getCustomer(id)
}`)
	wd2 := f2.Decls[0].(*syntax.WorkflowDecl)
	ce2 := wd2.Body.Stmts[0].(*syntax.ExprStmt).Value.(*syntax.ConcurrentExpr)
	if !ce2.PreserveOrder || ce2.Limit == nil {
		t.Fatalf("ce2 = %+v", ce2)
	}

	// Bare form: `preserveOrder` with no `limit:` clause at all.
	f3 := mustParse(t, `
workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {
    concurrent(id in ids, preserveOrder):
        getCustomer(id)
}`)
	wd3 := f3.Decls[0].(*syntax.WorkflowDecl)
	ce3 := wd3.Body.Stmts[0].(*syntax.ExprStmt).Value.(*syntax.ConcurrentExpr)
	if !ce3.PreserveOrder || ce3.Limit != nil {
		t.Fatalf("ce3 = %+v", ce3)
	}
}

func TestParseUnboundedLimitParsesButIsFlaggedForChecker(t *testing.T) {
	// D1: parses fine, checker-rejected — not this package's job.
	f := mustParse(t, `
workflow w(ids: List<CustomerId>) -> BatchResult<CustomerId, Customer, CustomerError> {
    concurrent(id in ids, limit: unbounded):
        getCustomer(id)
}`)
	wd := f.Decls[0].(*syntax.WorkflowDecl)
	stmt := wd.Body.Stmts[0].(*syntax.ExprStmt)
	ce := stmt.Value.(*syntax.ConcurrentExpr)
	if ce.Limit == nil || !ce.Limit.Unbounded {
		t.Fatalf("Limit = %+v, want Unbounded", ce.Limit)
	}
}

func TestParseRetry(t *testing.T) {
	f := mustParse(t, `
workflow w(id: CustomerId) -> Result<Customer, CustomerError> {
    return retry(attempts: 3): getCustomer(id)
}`)
	wd := f.Decls[0].(*syntax.WorkflowDecl)
	ret := wd.Body.Stmts[0].(*syntax.ReturnStmt)
	re := ret.Value.(*syntax.RetryExpr)
	attempts, ok := re.Attempts.(*syntax.IntLit)
	if !ok || attempts.Text != "3" {
		t.Fatalf("Attempts = %#v", re.Attempts)
	}
	body, ok := re.Body.(syntax.ExprBody)
	if !ok {
		t.Fatalf("Body = %#v, want ExprBody", re.Body)
	}
	if _, ok := body.Expr.(*syntax.CallExpr); !ok {
		t.Fatalf("Body.Expr = %#v, want CallExpr", body.Expr)
	}
}

func TestParseTransactionNesting(t *testing.T) {
	// transaction_error.fiig's doubleReserve: parses fine, checker-rejected (C2).
	f := mustParse(t, `
workflow w(orderId: OrderId) -> Result<None, OrderError> {
    transaction Database {
        reserveInventory(orderId)?
        transaction Database {
            releaseInventory(orderId)?
        }
    }
}`)
	wd := f.Decls[0].(*syntax.WorkflowDecl)
	outer := wd.Body.Stmts[0].(*syntax.TransactionStmt)
	if outer.Capability != "Database" {
		t.Fatalf("outer.Capability = %q", outer.Capability)
	}
	if len(outer.Body.Stmts) != 2 {
		t.Fatalf("got %d statements in outer transaction body", len(outer.Body.Stmts))
	}
	tryExpr := outer.Body.Stmts[0].(*syntax.ExprStmt).Value.(*syntax.TryExpr)
	if _, ok := tryExpr.Operand.(*syntax.CallExpr); !ok {
		t.Fatalf("tryExpr.Operand = %#v", tryExpr.Operand)
	}
	inner := outer.Body.Stmts[1].(*syntax.TransactionStmt)
	if inner.Capability != "Database" {
		t.Fatalf("inner.Capability = %q", inner.Capability)
	}
}

func TestParseIfElseIfElse(t *testing.T) {
	f := mustParse(t, `
rule classify(x: int) -> string {
    if x == 0 {
        return "zero"
    } else if x < 0 {
        return "negative"
    } else {
        return "positive"
    }
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ifs := rd.Body.Stmts[0].(*syntax.IfStmt)
	cmp := ifs.Cond.(*syntax.BinaryExpr)
	if cmp.Op != syntax.OpEq {
		t.Fatalf("Cond.Op = %v", cmp.Op)
	}
	if len(ifs.ElseIfs) != 1 {
		t.Fatalf("got %d else-if branches, want 1", len(ifs.ElseIfs))
	}
	if ifs.ElseIfs[0].Cond.(*syntax.BinaryExpr).Op != syntax.OpLt {
		t.Fatalf("else-if Cond = %+v", ifs.ElseIfs[0].Cond)
	}
	if ifs.Else == nil {
		t.Fatal("Else is nil, want a block")
	}
}

func TestParseImportPlainAndAliased(t *testing.T) {
	f := mustParse(t, `
use company.time as businessTime
use standard.time as systemTime

rule r() -> string {
    return "x"
}`)
	if len(f.Imports) != 2 {
		t.Fatalf("got %d imports", len(f.Imports))
	}
	if f.Imports[0].Alias != "businessTime" {
		t.Fatalf("import 0 = %+v", f.Imports[0])
	}
	if f.Imports[1].Alias != "systemTime" {
		t.Fatalf("import 1 = %+v", f.Imports[1])
	}

}

// TestWildcardImportIsAParseError is G2 since ADR 0050 §2.4: the grammar
// has no `.*` form. The parser recovers by keeping the path before it, so
// the declaration after the import still parses.
func TestWildcardImportIsAParseError(t *testing.T) {
	f, errs := Parse(`use standard.time.*

rule r(moment: time.Instant) -> string {
    return "instant"
}`)
	if len(errs) != 1 || errs[0].Rule != "G2" || errs[0].At.Line != 1 || errs[0].At.Column != 19 {
		t.Fatalf("errs = %+v, want one G2 at 1:19", errs)
	}
	if got := f.Imports[0].Path; len(got) != 2 || got[1] != "time" || len(f.Decls) != 1 {
		t.Fatalf("recovery: imports %+v, decls %d", f.Imports, len(f.Decls))
	}
}

func TestParseAdapterHttpWithResponseMapping(t *testing.T) {
	f := mustParse(t, `
adapter http customerHttpApiWithMapping {
    GET "/customers/{id}" -> readCustomer(id: CustomerId) {
        Ok(customer) -> 200 customer
        Err(error) -> customerErrorToHttpResponse(error)
    }
}`)
	ad := f.Decls[0].(*syntax.AdapterDecl)
	if ad.Kind != syntax.AdapterHTTP || len(ad.Routes) != 1 {
		t.Fatalf("adapter = %+v", ad)
	}
	route := ad.Routes[0]
	if route.Verb != "GET" || route.Path != "/customers/{id}" {
		t.Fatalf("route = %+v", route)
	}
	if route.Workflow.Name != "readCustomer" || len(route.Args) != 1 || route.Args[0].Name != "id" {
		t.Fatalf("workflow = %+v, args = %+v", route.Workflow, route.Args)
	}
	if len(route.Mapping) != 2 {
		t.Fatalf("got %d response arms, want 2", len(route.Mapping))
	}

	status, ok := route.Mapping[0].Value.(*syntax.StatusResponse)
	if !ok {
		t.Fatalf("arm 0 value = %#v, want StatusResponse", route.Mapping[0].Value)
	}
	if status.Code.(*syntax.IntLit).Text != "200" {
		t.Fatalf("status code = %+v", status.Code)
	}
	if status.Body == nil || status.Body.(*syntax.Ident).Name != "customer" {
		t.Fatalf("status body = %+v", status.Body)
	}

	exprResp, ok := route.Mapping[1].Value.(*syntax.ExprResponse)
	if !ok {
		t.Fatalf("arm 1 value = %#v, want ExprResponse", route.Mapping[1].Value)
	}
	call, ok := exprResp.Value.(*syntax.CallExpr)
	if !ok || call.Callee.(*syntax.Ident).Name != "customerErrorToHttpResponse" {
		t.Fatalf("arm 1 expr = %+v", exprResp.Value)
	}
}

func TestParseAdapterResponseMappingBareStatusCode(t *testing.T) {
	// A bare status code (no body expression) immediately followed by
	// another arm — the case that needs the arm-start lookahead to
	// disambiguate (see looksLikeArmStart).
	f := mustParse(t, `
adapter http api {
    GET "/x" -> f(id: CustomerId) {
        Ok(customer) -> 200 customer
        Err(CustomerError.NotFound(_)) -> 404
        Err(error) -> 500 error
    }
}`)
	ad := f.Decls[0].(*syntax.AdapterDecl)
	route := ad.Routes[0]
	if len(route.Mapping) != 3 {
		t.Fatalf("got %d arms, want 3", len(route.Mapping))
	}
	notFound, ok := route.Mapping[1].Value.(*syntax.StatusResponse)
	if !ok {
		t.Fatalf("arm 1 = %#v, want StatusResponse", route.Mapping[1].Value)
	}
	if notFound.Code.(*syntax.IntLit).Text != "404" {
		t.Fatalf("code = %+v", notFound.Code)
	}
	if notFound.Body != nil {
		t.Fatalf("Body = %+v, want nil (bare status code)", notFound.Body)
	}
	pat, ok := route.Mapping[1].Pattern.(*syntax.ConstructorPattern)
	if !ok || pat.Path[len(pat.Path)-1] != "Err" {
		t.Fatalf("pattern = %#v", route.Mapping[1].Pattern)
	}
	inner, ok := pat.Args[0].(*syntax.ConstructorPattern)
	if !ok || inner.Path[0] != "CustomerError" || inner.Path[1] != "NotFound" {
		t.Fatalf("inner pattern = %#v", pat.Args[0])
	}
}

// ADR 0069 §1 and §5: a route argument's type may be any type, read from a
// header or cookie, and the route declares its content types.
func TestParseRouteSourcesAndMediaTypes(t *testing.T) {
	f := mustParse(t, `
adapter http accountApi {
    POST "/orders/{id}" -> placeOrder(
        id: string,
        authorization: string from header "Authorization",
        requestId: string | None from header "X-Request-Id",
        session: string from cookie "sid",
        input: NewOrder,
    )
        consumes "application/json"
        produces "application/json"
    {
        Ok(order) -> 200 order
    }
    GET "/export" -> exportOrders() produces "text/csv"
}`)
	ad := f.Decls[0].(*syntax.AdapterDecl)
	route := ad.Routes[0]
	if len(route.Args) != 5 {
		t.Fatalf("got %d args, want 5", len(route.Args))
	}
	auth := route.Args[1]
	if auth.Source != "header" || auth.SourceName != "Authorization" {
		t.Fatalf("authorization = %+v", auth)
	}
	if _, ok := route.Args[2].Type.(*syntax.UnionType); !ok {
		t.Fatalf("requestId type = %#v, want a union", route.Args[2].Type)
	}
	if route.Args[3].Source != "cookie" || route.Args[3].SourceName != "sid" {
		t.Fatalf("session = %+v", route.Args[3])
	}
	if route.Args[4].Source != "" {
		t.Fatalf("input = %+v, want no source", route.Args[4])
	}
	if route.Consumes == nil || route.Consumes.Text != "application/json" || route.Produces == nil || route.Produces.Text != "application/json" {
		t.Fatalf("consumes = %+v, produces = %+v", route.Consumes, route.Produces)
	}
	if len(route.Mapping) != 1 {
		t.Fatalf("got %d arms, want 1", len(route.Mapping))
	}
	export := ad.Routes[1]
	if len(export.Args) != 0 || export.Produces == nil || export.Produces.Text != "text/csv" || export.Consumes != nil {
		t.Fatalf("export = %+v", export)
	}
}

func TestParseRouteBadSource(t *testing.T) {
	_, errs := Parse(`
adapter http api {
    GET "/x" -> f(a: string from query "a") produces "application/json"
}`)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for `from query`")
	}
}

func TestParseAdapterCli(t *testing.T) {
	f := mustParse(t, `
adapter cli customerCli {
    command "customer get {id}" -> readCustomer(id: CustomerId)
}`)
	ad := f.Decls[0].(*syntax.AdapterDecl)
	if ad.Kind != syntax.AdapterCLI {
		t.Fatalf("Kind = %v", ad.Kind)
	}
	route := ad.Routes[0]
	if route.Verb != "command" || route.Path != "customer get {id}" {
		t.Fatalf("route = %+v", route)
	}
	if route.Mapping != nil {
		t.Fatalf("Mapping = %+v, want nil (no mapping block)", route.Mapping)
	}
}

func TestParseUnionTypeAndRecordType(t *testing.T) {
	f := mustParse(t, `model M { middleName: string | None }`)
	md := f.Decls[0].(*syntax.ModelDecl)
	union, ok := md.Fields[0].Type.(*syntax.UnionType)
	if !ok || len(union.Members) != 2 {
		t.Fatalf("Type = %#v", md.Fields[0].Type)
	}

	f2 := mustParse(t, `rule r(input: { id: OrderId, total: Money }) -> string { return "x" }`)
	rd := f2.Decls[0].(*syntax.RuleDecl)
	rec, ok := rd.Params[0].Type.(*syntax.RecordType)
	if !ok || len(rec.Fields) != 2 {
		t.Fatalf("Type = %#v", rd.Params[0].Type)
	}
}

func TestParseComparisonAndBooleanOperators(t *testing.T) {
	f := mustParse(t, `
rule r(a: int, b: int) -> string {
    if a == b and not (a < b) or a >= b {
        return "x"
    }
    return "y"
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ifs := rd.Body.Stmts[0].(*syntax.IfStmt)
	// or-expression is the outermost: (a == b and not (a < b)) or (a >= b)
	or := ifs.Cond.(*syntax.BinaryExpr)
	if or.Op != syntax.OpOr {
		t.Fatalf("outermost op = %v, want or", or.Op)
	}
	and := or.Left.(*syntax.BinaryExpr)
	if and.Op != syntax.OpAnd {
		t.Fatalf("left of or = %v, want and", and.Op)
	}
	if _, ok := and.Right.(*syntax.NotExpr); !ok {
		t.Fatalf("right of and = %#v, want NotExpr", and.Right)
	}
}

// TestParseArithmeticPrecedence is ADR 0019: multiplicative binds tighter
// than additive, both bind tighter than comparison, matching every
// mainstream language's convention.
func TestParseArithmeticPrecedence(t *testing.T) {
	// No boolean type is attested anywhere yet (grammar.md §1), so this
	// stays inside an `if` condition rather than claiming some rule returns
	// one — same pattern TestParseComparisonAndBooleanOperators above uses.
	f := mustParse(t, `
rule r(a: int, b: int, c: int) -> int {
    if a + b * c == c {
        return c
    }
    return a
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ifs := rd.Body.Stmts[0].(*syntax.IfStmt)

	eq := ifs.Cond.(*syntax.BinaryExpr)
	if eq.Op != syntax.OpEq {
		t.Fatalf("outermost op = %v, want ==", eq.Op)
	}
	add := eq.Left.(*syntax.BinaryExpr)
	if add.Op != syntax.OpAdd {
		t.Fatalf("left of == = %v, want +", add.Op)
	}
	if _, ok := add.Left.(*syntax.Ident); !ok {
		t.Fatalf("left of + = %#v, want a plain Ident (a)", add.Left)
	}
	mul := add.Right.(*syntax.BinaryExpr)
	if mul.Op != syntax.OpMul {
		t.Fatalf("right of + = %v, want * (b * c nested tighter)", mul.Op)
	}
}

// TestParseUnaryMinusBindsTighterThanMultiplicativeLooserThanPostfix is
// ADR 0019: `-a.b * c` is `(-(a.b)) * c`, not `-(a.b * c)` and not
// `(-a).b`.
func TestParseUnaryMinusBindsTighterThanMultiplicativeLooserThanPostfix(t *testing.T) {
	f := mustParse(t, `
rule r(a: Thing, c: int) -> int {
    return -a.b * c
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ret := rd.Body.Stmts[0].(*syntax.ReturnStmt)

	mul := ret.Value.(*syntax.BinaryExpr)
	if mul.Op != syntax.OpMul {
		t.Fatalf("outermost op = %v, want *", mul.Op)
	}
	neg, ok := mul.Left.(*syntax.NegExpr)
	if !ok {
		t.Fatalf("left of * = %#v, want NegExpr", mul.Left)
	}
	if _, ok := neg.Operand.(*syntax.FieldAccess); !ok {
		t.Fatalf("operand of - = %#v, want FieldAccess (a.b), not just a", neg.Operand)
	}
}

// TestParseNotBindsTighterThanPostfixOperand is `not`'s own version of
// TestParseUnaryMinusBindsTighterThanMultiplicativeLooserThanPostfix: `not
// input.email.value.contains("@")` (validate_new_customer.fiig) must mean
// `not (input.email.value.contains("@"))`, not `(not input).email.value...`.
func TestParseNotBindsTighterThanPostfixOperand(t *testing.T) {
	f := mustParse(t, `
rule r(input: Thing) -> int {
    if not input.email.value.contains("@") {
        return 1
    }
    return 0
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	ifs := rd.Body.Stmts[0].(*syntax.IfStmt)
	not, ok := ifs.Cond.(*syntax.NotExpr)
	if !ok {
		t.Fatalf("Cond = %#v, want NotExpr", ifs.Cond)
	}
	call, ok := not.Operand.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("not.Operand = %#v, want CallExpr (...contains(\"@\")), not just `input`", not.Operand)
	}
	fa, ok := call.Callee.(*syntax.FieldAccess)
	if !ok || fa.Field != "contains" {
		t.Fatalf("call.Callee = %#v, want FieldAccess(..., \"contains\")", call.Callee)
	}
}

// TestSecondPublicDeclIsAParseError is G1 since ADR 0054: a second public
// declaration is rejected by the grammar, at the declaration itself, but is
// still parsed so the rest of the file can be checked.
func TestSecondPublicDeclIsAParseError(t *testing.T) {
	f, errs := Parse(`
model ShippingAddress {
    line1: string
}

model BillingAddress {
    line1: string
}`)
	if len(errs) != 1 || errs[0].Rule != "G1" || errs[0].At.Line != 6 {
		t.Fatalf("want one second-public-declaration error at line 6, got %v", errs)
	}
	if len(f.Decls) != 2 {
		t.Fatalf("got %d decls, want 2 (the second kept for checking)", len(f.Decls))
	}
}

func TestPrivateBlockParsesHelpersAndModels(t *testing.T) {
	f, errs := Parse(`rule fooBar(number: int) -> int {
    if interiorFunctionCall(number) {
        return 3
    }
    return 4
}

private {
    interiorFunctionCall(number: int) -> bool {
        return number > 0
    }

    model Threshold {
        minimum: int
    }
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(f.Decls) != 1 || f.Private == nil || len(f.Private.Members) != 2 {
		t.Fatalf("want one public decl and two private members, got %d decls, private %+v", len(f.Decls), f.Private)
	}
	h, ok := f.Private.Members[0].(*syntax.HelperDecl)
	if !ok || h.Name != "interiorFunctionCall" || len(h.Params) != 1 {
		t.Fatalf("member 0 = %#v, want helper interiorFunctionCall(number)", f.Private.Members[0])
	}
	if m, ok := f.Private.Members[1].(*syntax.ModelDecl); !ok || m.Name != "Threshold" {
		t.Fatalf("member 1 = %#v, want model Threshold", f.Private.Members[1])
	}
}

// TestPrivateBlockStructuralErrors covers ADR 0054 §1's rules the grammar
// enforces: what may hold a private block, how many, and what goes in one.
func TestPrivateBlockStructuralErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"after a model", `model M {
    x: int
}
private {
}
`, "may not have a private block"},
		{"before the public decl", `private {
}
rule r() -> int {
    return 1
}
`, "must follow"},
		{"twice", `rule r() -> int {
    return 1
}
private {
}
private {
}
`, "only one private block"},
		{"decl after the block", `rule r() -> int {
    return 1
}
private {
}
rule s() -> int {
    return 2
}
`, "second public declaration"},
		{"kind keyword on a helper", `rule r() -> int {
    return 1
}
private {
    rule h() -> int {
        return 2
    }
}
`, "no kind keyword"},
		{"enum member", `rule r() -> int {
    return 1
}
private {
    enum E {
        A
    }
}
`, "enums are not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := Parse(tc.src)
			for _, e := range errs {
				if strings.Contains(e.Msg, tc.want) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.want, errs)
		})
	}
}

func TestSyntaxErrorIsReported(t *testing.T) {
	_, errs := Parse(`model {`)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for a model with no name")
	}
}

// TestCorpusParsesWithoutErrors mirrors the lexer's corpus smoke test:
// Milestone 1's exit criteria requires every Milestone 0 example to parse.
// This includes invalid-programs/*.fiig — those are semantically invalid,
// not syntactically invalid, per the parse/check split used throughout the
// grammar sketch and bug-class catalog. The one exception is that most of
// them pack several declarations into one file, which ADR 0054 made a
// parse error; that error, and only that one, is allowed there.
func TestCorpusParsesWithoutErrors(t *testing.T) {
	root := findExamplesDir(t)

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".fiig" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .fiig files found under %s", root)
	}

	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			file, errs := Parse(string(src))
			if filepath.Base(filepath.Dir(f)) == "invalid-programs" {
				errs = withoutTaggedParseErrors(errs)
			}
			if len(errs) > 0 {
				t.Fatalf("%d parse error(s) in %s: %v", len(errs), f, errs)
			}
			if len(file.Decls) == 0 {
				t.Fatalf("%s parsed with zero declarations", f)
			}
		})
	}
}

func findExamplesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find examples/ directory above " + dir)
		}
		dir = parent
	}
}

// withoutTaggedParseErrors drops the parse errors that are numbered bug
// classes — a second public declaration (G1, ADR 0054) and a wildcard
// import (G2, ADR 0050 §2.4) — which the invalid-programs fixtures trip on
// purpose.
func withoutTaggedParseErrors(errs []Error) []Error {
	var out []Error
	for _, e := range errs {
		if e.Rule != "G1" && e.Rule != "G2" {
			out = append(out, e)
		}
	}
	return out
}

// TestListLiteral is ADR 0057 §1: `[a, b]`, `[]`, and a trailing comma
// across lines.
func TestListLiteral(t *testing.T) {
	f := mustParse(t, `rule r(x: int) -> List<int> {
    a = []
    b = [x, x + 1]
    return [
        x,
        2,
    ]
}`)
	stmts := f.Decls[0].(*syntax.RuleDecl).Body.Stmts
	for i, want := range []int{0, 2} {
		lit, ok := stmts[i].(*syntax.Binding).Value.(*syntax.ListLit)
		if !ok || len(lit.Elems) != want {
			t.Fatalf("statement %d: want a %d-element ListLit, got %#v", i, want, stmts[i].(*syntax.Binding).Value)
		}
	}
	ret := stmts[2].(*syntax.ReturnStmt).Value.(*syntax.ListLit)
	if len(ret.Elems) != 2 || ret.Start.Line != 4 || ret.End.Line != 7 {
		t.Fatalf("want a 2-element literal spanning lines 4-7, got %d elements, %+v", len(ret.Elems), ret.Span)
	}

	if _, errs := Parse("rule r() -> List<int> {\n    return [1, 2\n}"); len(errs) == 0 {
		t.Fatal("an unclosed list literal must be a parse error")
	}
}

// TestBinaryPatternAndExpression is ADR 0058: `<<` opens, `>>` closes as
// two `>` tokens, hex literals, and segments on both sides.
func TestBinaryPatternAndExpression(t *testing.T) {
	f := mustParse(t, `conversion c(buf: bytes) -> bytes {
    match buf {
        <<0x52: u8, n: i32be, body: bytes(n - 4), _: u4, "ok": utf8, rest: bytes>> -> <<n + 1: u16le, body: bytes>>
        _ -> <<>>
    }
}`)
	m := f.Decls[0].(*syntax.ConversionDecl).Body.Stmts[0].(*syntax.MatchStmt)
	pat := m.Arms[0].Pattern.(*syntax.BytesPattern)
	if len(pat.Segments) != 6 {
		t.Fatalf("want 6 segments, got %d", len(pat.Segments))
	}
	if lit := pat.Segments[0].Literal.(*syntax.IntLit); lit.Text != "0x52" {
		t.Fatalf("literal = %q", lit.Text)
	}
	enc := pat.Segments[1].Encoding
	if enc.Bits != 32 || !enc.Signed || enc.Little {
		t.Fatalf("i32be parsed as %+v", enc)
	}
	if pat.Segments[2].Encoding.Kind != syntax.SegBytes || pat.Segments[2].Encoding.Size == nil {
		t.Fatalf("bytes(n - 4) parsed as %+v", pat.Segments[2].Encoding)
	}
	if pat.Segments[3].Name != "_" || pat.Segments[3].Encoding.Bits != 4 {
		t.Fatalf("_: u4 parsed as %+v", pat.Segments[3])
	}
	built := m.Arms[0].Body.(syntax.ExprBody).Expr.(*syntax.BytesLit)
	if len(built.Segments) != 2 || !built.Segments[0].Encoding.Little {
		t.Fatalf("expression segments = %+v", built.Segments)
	}
	if empty := m.Arms[1].Body.(syntax.ExprBody).Expr.(*syntax.BytesLit); len(empty.Segments) != 0 {
		t.Fatalf("<<>> has %d segments", len(empty.Segments))
	}

	// Nested generics still close with two separate `>` tokens.
	mustParse(t, "rule r(x: List<List<int>>) -> int {\n    return 0\n}")
}

// ADR 0071 §1: a config declaration's fields, their variables, and
// defaults.
func TestParseConfigDecl(t *testing.T) {
	f, errs := Parse(`config Sessions {
    maxAge: int from env "SESSION_MAX_AGE" = 24 * 60 * 60
    exportToken: Secret from env "ACCOUNTS_EXPORT_TOKEN"
    level: Level | None from env "LOG_LEVEL"
}`)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	d := f.Decls[0].(*syntax.ConfigDecl)
	if d.Name != "Sessions" || len(d.Fields) != 3 {
		t.Fatalf("decl = %+v", d)
	}
	if d.Fields[0].Env != "SESSION_MAX_AGE" || d.Fields[0].Default == nil || d.Fields[1].Default != nil || d.Fields[2].Env != "LOG_LEVEL" {
		t.Fatalf("fields = %+v", d.Fields)
	}
	if _, errs := Parse("config C {\n    a: int\n}"); len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected `from env \"NAME\"`") {
		t.Fatalf("missing from env: %v", errs)
	}
	if _, errs := Parse("config C {\n    a: int from env \"A${x}\"\n}"); len(errs) == 0 {
		t.Fatal("interpolated variable name parsed")
	}
	// config is reserved only where a declaration starts.
	if _, errs := Parse("rule r(config: int) -> int {\n    return config\n}"); len(errs) != 0 {
		t.Fatalf("config as a name: %v", errs)
	}
}
