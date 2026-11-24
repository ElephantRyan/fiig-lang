package format

import "testing"

// TestSyntaxFormSnapshots is the Milestone 1 deliverable "snapshot tests for
// every syntax form": one case per production in docs/specification/
// grammar.md, each pinning its exact canonical text. Every case's src is
// already canonical — if Format changes a case's output, the diff below is
// the regression report.
//
// Most cases are copied verbatim from examples/*.fiig files already
// confirmed byte-identical to their own Format output (so this doubles as
// proof those files stay canonical); a few forms the current corpus leaves
// non-canonical (concurrent/retry bodies collapse to one line; match block
// arms expand to multiple) use the real Format output for an equivalent
// snippet instead of the stale corpus spelling — see
// TestFormatIsIdempotent's corpus-wide pass for why those files differ. A
// couple of forms grammar.md flags as syntactically legal but unattested
// anywhere (a `None`-unioned field, multi-line call arguments)
// get a minimal, self-contained snapshot here rather than a guess left
// unverified, since Format itself is the ground truth being pinned.
func TestSyntaxFormSnapshots(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "model declaration (multiple fields)",
			src: `model Customer {
    id: CustomerId;
    name: string;
    email: EmailAddress;
}
`,
		},
		{
			name: "enum declaration (multiple variants)",
			src: `enum CustomerError {
    NotFound(id: CustomerId),
    EmailAlreadyInUse(email: EmailAddress),
    Invalid(field: string, reason: string)
}
`,
		},
		{
			name: "rule declaration (if guard, not, field access, method call)",
			src: `rule validateNewCustomer(input: NewCustomer) -> Result<NewCustomer, CustomerError> {
    if (input.name.isBlank()) {
        return Err(CustomerError.Invalid("name", "must not be blank"));
    }

    if (!input.email.value.contains("@")) {
        return Err(CustomerError.Invalid("email", "must be a valid address"));
    }

    return Ok(input);
}
`,
		},
		{
			name: "conversion declaration",
			src: `conversion customerToSummary(customer: Customer) -> CustomerSummary {
    return CustomerSummary(id: customer.id, name: customer.name);
}
`,
		},
		{
			name: "gateway declaration with idempotent, match on T | None",
			src: `gateway getCustomer(id: CustomerId) -> Result<Customer, CustomerError>
        requires Database
        idempotent {
    row = Database.queryOne("SELECT id, name, email FROM customers WHERE id = ?", id);
    return match (row) {
        found -> Ok(Customer(id: found.id, name: found.name, email: found.email)),
        None -> Err(CustomerError.NotFound(id))
    }
}
`,
		},
		{
			name: "gateway declaration without idempotent",
			src: `gateway checkEmailUnique(email: EmailAddress) -> Result<None, CustomerError> requires Database {
    existing = Database.queryOne("SELECT id FROM customers WHERE email = ?", email);
    return match (existing) {
        found -> Err(CustomerError.EmailAlreadyInUse(email)),
        None -> Ok(None)
    }
}
`,
		},
		{
			name: "gateway declaration writing through a capability",
			src: `gateway updateCustomerEmail(id: CustomerId, email: EmailAddress) -> Result<None, CustomerError>
        requires Database {
    Database.execute("UPDATE customers SET email = ? WHERE id = ?", email, id);
    return Ok(None);
}
`,
		},
		{
			name: "workflow declaration, bare call return",
			src: `workflow readCustomer(id: CustomerId) -> Result<Customer, CustomerError> {
    return getCustomer(id);
}
`,
		},
		{
			name: "transaction block with ? propagation",
			src: `workflow changeCustomerEmail(id: CustomerId, newEmail: EmailAddress)
        -> Result<Customer, CustomerError> {
    transaction Database {
        updateCustomerEmail(id, newEmail)?;
        return getCustomer(id);
    }
}
`,
		},
		{
			name: "retry expression",
			src: `workflow readCustomerWithRetry(id: CustomerId) -> Result<Customer, CustomerError> {
    return retry(attempts: 3): getCustomer(id);
}
`,
		},
		{
			name: "concurrent expression, bare form",
			src: `workflow retrieveCustomers(customerIds: List<CustomerId>)
        -> BatchResult<CustomerId, Customer, CustomerError> {
    return concurrent(customerId in customerIds): getCustomer(customerId);
}
`,
		},
		{
			name: "concurrent expression, explicit limit",
			src: `workflow retrieveCustomersLimited(customerIds: List<CustomerId>)
        -> BatchResult<CustomerId, Customer, CustomerError> {
    return concurrent(customerId in customerIds, limit: 5): getCustomer(customerId);
}
`,
		},
		{
			name: "concurrent expression, unbounded limit (checker-rejected, parses fine)",
			src: `workflow retrieveAllCustomers(customerIds: List<CustomerId>)
        -> BatchResult<CustomerId, Customer, CustomerError> {
    return concurrent(customerId in customerIds, limit: unbounded): getCustomer(customerId);
}
`,
		},
		{
			name: "match with block-bodied arms and a qualified constructor pattern",
			src: `workflow checkout(orderId: OrderId, input: NewCustomer, total: Money)
        -> Result<Order, CheckoutError> {
    return match (createCustomer(input)) {
        Err(error) -> Err(CheckoutError.Customer(error)), // wrapped unchanged
        found -> {
            return match (placeOrder(orderId, found.id, total)) {
                Ok(order) -> Ok(order),
                Err(OrderError.InventoryUnavailable(_)) -> Err(CheckoutError.OutOfStock), // reinterpreted
                Err(error) -> Err(CheckoutError.Order(error)) // wrapped unchanged
            }
        }
    }
}
`,
		},
		{
			name: "arithmetic operators (int-only, via a rule)",
			src: `// ADR 0019: arithmetic is int-only and never overloaded — adding two Money
// amounts is an ordinary rule, not a + defined on Money itself.
rule addMoney(a: Money, b: Money) -> Money {
    return Money(a.value + b.value);
}
`,
		},
		{
			name: "adapter http route, path placeholder",
			src: `// Required example 8 (HTTP half): expose readCustomer over HTTP.
adapter http customerHttpApi {
    GET "/customers/{id}" -> readCustomer(id: CustomerId) produces "application/json";
}
`,
		},
		{
			name: "adapter http route, path and query placeholders, no body",
			src: `// ADR 0021: a path placeholder and a query placeholder bound on the same
// route, both by name match exactly like ADR 0009's path-only case —
// nothing is left unclaimed, so this route has no body.
adapter http customerEmailApi {
    PATCH "/customers/{id}?newEmail={newEmail}" -> changeCustomerEmail(
        id: CustomerId,
        newEmail: EmailAddress
    )
            produces "application/json";
}
`,
		},
		{
			name: "adapter http route, POST with request-body binding",
			src: `// ADR 0021: POST binds its one unclaimed parameter from the request body.
// input isn't claimed by any path or query placeholder, and NewCustomer
// is a model, so this is real structural decoding, not a raw pass-through.
adapter http customerCreateApi {
    POST "/customers" -> createCustomer(input: NewCustomer)
            consumes "application/json"
            produces "application/json";
}
`,
		},
		{
			name: "adapter http route, QUERY with request-body binding",
			src: `// ADR 0021: QUERY (RFC 10008) carries a body like POST but stays safe and
// idempotent like GET. criteria is unclaimed by the path, so it binds
// from the request body — CustomerLookup is a model, so this is real
// structural decoding.
adapter http customerQueryApi {
    QUERY "/customers/lookup" -> lookupCustomer(criteria: CustomerLookup)
            consumes "application/json"
            produces "application/json";
}
`,
		},
		{
			name: "adapter http route, header and cookie arguments, non-JSON body",
			src: `// ADR 0069: arguments read from a header or cookie, and a raw text body.
adapter http importApi {
    POST "/imports" -> importOrders(
        csv: string,
        token: string from header "Authorization",
        session: string | None from cookie "sid"
    )
            consumes "text/csv"
            produces "application/json";
    GET "/exports" -> exportOrders() produces "text/csv";
}
`,
		},
		{
			name: "adapter cli route",
			src: `// Required example 8 (CLI half): expose the same readCustomer workflow over
// a CLI adapter — no logic is duplicated between the two adapters.
adapter cli customerCli {
    command "customer get {id}" -> readCustomer(id: CustomerId);
}
`,
		},
		{
			name: "adapter response-mapping block",
			src: `// ADR 0017: explicit per-route response mapping. The Ok arm uses the bare
// status-code shorthand; the Err arm reuses customerErrorToHttpResponse
// instead of repeating its mapping inline.
adapter http customerHttpApiWithMapping {
    GET "/customers/{id}" -> readCustomer(id: CustomerId) produces "application/json" {
        Ok(customer) -> 200 customer,
        Err(error) -> customerErrorToHttpResponse(error)
    }
}
`,
		},
		{
			name: "imports: aliased and a qualified type reference",
			src: `// Required example 9: import two aliased time libraries. Both packages are
// named "time"; the aliases are the only thing that make them referenceable
// in the same file, and each keeps its own canonical registry identity.
use company.time as businessTime;
use standard.time as systemTime;

rule computeFollowUpDate(from: systemTime.Instant) -> systemTime.Instant {
    return businessTime.addBusinessDays(from, 3);
}
`,
		},
		{
			name: "anonymous record type in a public signature (checker-rejected, parses fine)",
			src: `// Type error: a structurally-typed anonymous record used in a public
// signature — structural typing is only for anonymous *local* records (§3.3).
rule summarizeOrderShape(input: { id: OrderId, total: Money }) -> string {
    return "Order ${input.id}: ${input.total}"; // rejected: public rule signature uses a structural type instead of a named model
}
`,
		},
		{
			name: "union type with None on a model field (legal per §3.5, unattested elsewhere)",
			src: `model Contact {
    middleName: string | None;
}
`,
		},
		{
			name: "boolean/comparison operators: not, and, parenthesized primary",
			src: `rule isUsableName(name: string) -> string {
    if (!(name == "") && name != "unknown") {
        return name;
    }

    return "empty";
}
`,
		},
		{
			name: "a multi-line call's arguments",
			src: `gateway insertCustomer(input: NewCustomer) -> Result<Customer, CustomerError> requires Database {
    id = CustomerId(Database.nextId());
    Database.execute(
        "INSERT INTO customers (id, name, email) VALUES (?, ?, ?)",
        id,
        input.name,
        input.email
    );
    return Ok(Customer(id: id, name: input.name, email: input.email));
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errs := Format(tc.src)
			if len(errs) > 0 {
				t.Fatalf("unexpected parse/format errors: %v", errs)
			}
			if out != tc.src {
				t.Fatalf("not canonical — snapshot mismatch:\ngot:\n%s\nwant:\n%s", out, tc.src)
			}
		})
	}
}
