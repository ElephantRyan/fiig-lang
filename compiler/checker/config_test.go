package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0071: config declarations, where they may be read, and Secret.

const sessionsConfig = `wire Sessions {
    maxAge: int from env "SESSION_MAX_AGE" = 24 * 60 * 60;
    exportToken: Secret from env "ACCOUNTS_EXPORT_TOKEN";
    level: Level | None from env "LOG_LEVEL";
    name: string from env "SERVICE_NAME" = "accounts";
    rate: decimal from env "RATE" = 1.5;
    verbose: bool from env "VERBOSE" = false;
    fallback: Level from env "FALLBACK_LEVEL" = Level.Info;
}`

const levelEnum = `enum Level {
    Debug,
    Info,
}`

func TestConfigReadInActivitiesAndGateways(t *testing.T) {
	diags := checkSrc(t, sessionsConfig, levelEnum, `activity lifetime(token: string) -> int {
    if (Sessions.exportToken.matches(token)) {
        return Sessions.maxAge;
    }
    return 0;
}`, `gateway send(host: string) -> Result<Connection, SocketError>
    requires Socket
{
    header = "Bearer ${Sessions.exportToken.reveal()}";
    return Socket.connect(host, Sessions.maxAge);
}`, `rule allowed(secret: Secret, token: string) -> bool {
    return secret.matches(token);
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestConfigOutsideActivitiesAndGatewaysIsB10(t *testing.T) {
	cases := []struct{ src, fragment string }{
		{"rule r() -> int {\n    return Sessions.maxAge;\n}", "a rule may not read Sessions.maxAge; take the value as a parameter"},
		{"conversion c() -> int {\n    return Sessions.maxAge;\n}", "a conversion may not read Sessions.maxAge"},
		{"workflow w() -> Result<int, string> {\n    return Ok(Sessions.maxAge);\n}", "a workflow may not read Sessions.maxAge"},
		{"formulas f {\n    formula g() -> int {\n        return Sessions.maxAge;\n    }\n}", "a formula may not read Sessions.maxAge"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, sessionsConfig, levelEnum, c.src), "B10", c.fragment)
	}
	wantDiag(t, checkSrc(t, sessionsConfig, levelEnum, "activity a() -> int {\n    return Sessions.lifetime;\n}"), "", `config Sessions has no field "lifetime"`)
}

func TestConfigDeclarationsAreV1(t *testing.T) {
	cases := []struct{ field, rule, fragment string }{
		{`data: bytes from env "DATA"`, "V1", "is bytes; a config value is string, int, float, decimal, bool, an enum, or Secret"},
		{`items: List<string> from env "ITEMS"`, "V1", "a config value is string"},
		{`key: Secret from env "KEY" = "hunter2"`, "V1", "is a Secret, so it has no default"},
		{`port: int from env "PORT" = Clock.now()`, "V1", "default must be a constant"},
		{`port: int from env "PORT" = "eighty"`, "A1", ""},
		{`port: int from env "1PORT"`, "V1", "isn't an environment variable name"},
		{`port: int from env "MY-PORT"`, "V1", "isn't an environment variable name"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, "wire Settings {\n    "+c.field+"\n}"), c.rule, c.fragment)
	}
	wantDiag(t, checkSrc(t, "wire Settings {\n    a: int from env \"A\";\n    a: int from env \"B\";\n}"), "V1", `declares field "a" twice`)
	wantDiag(t, checkSrc(t, "wire settings {\n    a: int from env \"A\";\n}"), "N1", `wire "settings" must be capitalized`)
}

func TestSecretIsNeverShown(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		{"activity a() -> string {\n    return \"key=${Sessions.exportToken}\";\n}", "S1", "a Secret can't be put in a string"},
		{"activity a(token: Secret) -> bool {\n    return token == Sessions.exportToken;\n}", "M1", "a Secret (compare one with .matches())"},
		{"activity a() -> string {\n    return Sessions.exportToken.reveal();\n}", "S2", "only a gateway may call .reveal()"},
		{"rule r(token: Secret) -> string {\n    return token.reveal();\n}", "S2", "only a gateway may call .reveal()"},
		{"workflow w(token: Secret) -> Result<string, string> {\n    return Ok(\"x\");\n}", "S1", "parameter token holds a Secret"},
		{"model Creds {\n    key: Secret;\n}", "", ""},
	}
	for _, c := range cases[:5] {
		wantDiag(t, checkSrc(t, sessionsConfig, levelEnum, c.src), c.rule, c.fragment)
	}
	// A model may hold one, to pass credentials between gateways; it just
	// can't reach a workflow's signature.
	if diags := checkSrc(t, cases[5].src); len(diags) != 0 {
		t.Fatalf("model holding a Secret: %v", diags)
	}
	wantDiag(t, checkSrc(t, cases[5].src, "workflow w() -> Result<Creds, string> {\n    return Err(\"x\");\n}"), "S1", "the return type holds a Secret")
	wantDiag(t, checkSrc(t, cases[5].src, "conversion c(creds: Creds) -> HttpResponse<Creds> {\n    return HttpResponse(status: 200, body: creds);\n}"), "S1", "a response body holds a Secret")
}

// ADR 0126: a gateway marks a value it reads from outside as a Secret, so the
// S1 and S2 protections hold from there on; anywhere else it is S3.
func TestToSecretIsLegalOnlyInAGateway(t *testing.T) {
	gateway := "gateway storedToken(saved: string) -> Secret\n    requires Database\n{\n    return saved.toSecret(\"abs.serviceToken\");\n}"
	if diags := checkSrc(t, gateway); len(diags) != 0 {
		t.Fatalf("toSecret in a gateway: %v", diags)
	}
	for _, src := range []string{
		"activity a(saved: string) -> Secret {\n    return saved.toSecret(\"abs.serviceToken\");\n}",
		"rule r(saved: string) -> Secret {\n    return saved.toSecret(\"abs.serviceToken\");\n}",
	} {
		wantDiag(t, checkSrc(t, src), "S3", "only a gateway may call .toSecret()")
	}
	// The result is as protected as one from config.
	wantDiag(t, checkSrc(t, "activity a(token: Secret) -> string {\n    return \"key=${token}\";\n}"), "S1", "a Secret can't be put in a string")
}

func TestSecretIsAPreludeName(t *testing.T) {
	wantDiag(t, checkSrc(t, "model Secret {\n    a: int;\n}"), "K2", "")
}

// A cross-package call checks its arguments where they were written: a
// constructor from another package given this package's config and enum.
// Before ADR 0071 this resolved the arguments in the callee's package, so
// any package-level name in one was undefined.
func TestCrossPackageArgumentsResolveInTheCaller(t *testing.T) {
	httpPkg := buildDep(t, "acme.http", nil, nil, "model WebRequest {\n    host: string;\n    authorization: string | None;\n}")
	app := types.Packages{"acme.http": httpPkg}
	f := mustParse(t, "use acme.http;\n\ngateway send() -> http.WebRequest\n    requires Socket\n{\n    return http.WebRequest(host: Region.Eu.host, authorization: \"Bearer ${Account.key.reveal()}\");\n}")
	files := []*syntax.File{f,
		mustParse(t, "wire Account {\n    key: Secret from env \"KEY\";\n}"),
		mustParse(t, "enum Region(host: string) {\n    Eu(host: \"eu.example.com\"),\n}"),
	}
	if diags := Check(files, app); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
}
