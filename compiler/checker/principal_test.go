package checker

import (
	"strings"
	"testing"
)

const (
	prIdentity  = "model Identity {\n    userId: string\n}"
	prRefusal   = "enum IdentityError {\n    NotSignedIn\n}"
	prBinder    = "workflow signedIn(token: string | None) -> Result<Identity, IdentityError> {\n    return Err(IdentityError.NotSignedIn)\n}"
	prSignIn    = "conversion signInFirst(error: IdentityError) -> HttpResponse<string> {\n    return HttpResponse(status: 401, body: \"sign in first\")\n}"
	prFeature   = "workflow showMine(me: Identity) -> string {\n    return me.userId\n}"
	prOpen      = "workflow showCover(id: string) -> string {\n    return id\n}"
	prPrincipal = "    principal me: Identity from cookie \"sid\" by signedIn else signInFirst\n"
)

func principalAdapter(body string) string {
	return "adapter http api {\n" + body + "}"
}

func checkPrincipals(t *testing.T, adapter string, extra ...string) []string {
	t.Helper()
	srcs := append([]string{adapter, prIdentity, prRefusal, prBinder, prSignIn, prFeature, prOpen}, extra...)
	diags := checkSrc(t, srcs...)
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.String()
	}
	return out
}

func wantOnly(t *testing.T, got []string, code, text string) {
	t.Helper()
	if len(got) != 1 || !strings.Contains(got[0], "["+code+"]") || !strings.Contains(got[0], text) {
		t.Fatalf("diags = %v, want one %s containing %q", got, code, text)
	}
}

// ADR 0137: a principal settled by a workflow, refused by a conversion,
// passed to a route by name; a public route takes none.
func TestAPrincipalChecks(t *testing.T) {
	got := checkPrincipals(t, principalAdapter(prPrincipal+
		"    GET \"/mine\" -> showMine(me)\n        produces \"application/json\"\n"+
		"    GET \"/covers/{id}\" public -> showCover(id: string)\n        produces \"application/json\"\n"))
	if len(got) != 0 {
		t.Fatalf("diags = %v, want none", got)
	}
}

func TestU1TheBinderIsAWorkflowOfTheRightShape(t *testing.T) {
	route := "    GET \"/mine\" -> showMine(me)\n        produces \"application/json\"\n"
	got := checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by signInFirst else signInFirst\n"+route))
	wantOnly(t, got, "U1", "a principal is settled by a workflow")

	got = checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by takesInt else signInFirst\n"+route),
		"workflow takesInt(token: int) -> Result<Identity, IdentityError> {\n    return Err(IdentityError.NotSignedIn)\n}")
	wantOnly(t, got, "U1", "takes int, but principal me gives it the cookie \"sid\"")

	got = checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by settlesString else signInFirst\n"+route),
		"workflow settlesString(token: string | None) -> Result<string, IdentityError> {\n    return Ok(\"x\")\n}")
	wantOnly(t, got, "U1", "settles string, but principal me is Identity")

	got = checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by nobody else signInFirst\n"+route))
	wantOnly(t, got, "U1", "no declaration named nobody")
}

func TestU2TheRefusalIsAConversionToAnHttpResponse(t *testing.T) {
	route := "    GET \"/mine\" -> showMine(me)\n        produces \"application/json\"\n"
	got := checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by signedIn else wrongError\n"+route),
		"enum Other {\n    X\n}",
		"conversion wrongError(error: Other) -> HttpResponse<string> {\n    return HttpResponse(status: 401, body: \"no\")\n}")
	wantOnly(t, got, "U2", "takes Other, but signedIn's workflow refuses with IdentityError")

	got = checkPrincipals(t, principalAdapter("    principal me: Identity from cookie \"sid\" by signedIn else signedIn\n"+route))
	wantOnly(t, got, "U2", "it is a conversion")
}

func TestU3EveryRouteTakesAPrincipalOrIsPublic(t *testing.T) {
	got := checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/covers/{id}\" -> showCover(id: string)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U3", "takes no principal; adapter api settles who is calling, so pass one (me), or mark the route `public`")

	got = checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/mine\" public -> showMine(me)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U3", "is public but takes me")

	got = checkPrincipals(t, principalAdapter("    GET \"/covers/{id}\" public -> showCover(id: string)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U3", "declares no principal, so every route is")
}

func TestU4ARouteNamesADeclaredPrincipalOfTheParametersType(t *testing.T) {
	got := checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/mine\" -> showMine(you)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U4", "route argument you has no type and names no principal of this adapter; write you: Type, or pass one of its principals (me)")

	got = checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/covers/{id}\" -> showCover(id)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U4", "route argument id has no type")
}

func TestU5APrincipalIsSettledFromAnEarlierOne(t *testing.T) {
	admin := []string{
		"model Admin {\n    userId: string\n}",
		"enum AdminRefusal {\n    NotAnAdmin\n}",
		"workflow signedInAdmin(me: Identity) -> Result<Admin, AdminRefusal> {\n    return Err(AdminRefusal.NotAnAdmin)\n}",
		"conversion notAnAdmin(error: AdminRefusal) -> HttpResponse<string> {\n    return HttpResponse(status: 403, body: \"admins only\")\n}",
		"workflow showSettings(admin: Admin) -> string {\n    return admin.userId\n}",
	}
	route := "    GET \"/settings\" -> showSettings(admin)\n        produces \"application/json\"\n"
	got := checkPrincipals(t, principalAdapter(prPrincipal+"    principal admin: Admin from me by signedInAdmin else notAnAdmin\n"+route), admin...)
	if len(got) != 0 {
		t.Fatalf("a role from me: diags = %v, want none", got)
	}
	got = checkPrincipals(t, principalAdapter("    principal admin: Admin from me by signedInAdmin else notAnAdmin\n"+prPrincipal+route), admin...)
	wantOnly(t, got, "U5", "settled from me, which isn't a principal declared before it")
}

func TestU6OnlyAnHttpAdapterSettlesAPrincipal(t *testing.T) {
	got := checkPrincipals(t, "adapter cli tools {\n"+prPrincipal+"    command \"cover {id}\" -> showCover(id: string)\n}")
	wantOnly(t, got, "U6", "a cli adapter settles no principal")
}

func TestU4APlaceholderCantShareAPrincipalsName(t *testing.T) {
	got := checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/users/{me}\" -> showMine(me)\n        produces \"application/json\"\n"))
	wantOnly(t, got, "U4", "\"me\" is bound by a path placeholder and is also a principal")
}

func TestU4APrincipalHasTheParametersType(t *testing.T) {
	got := checkPrincipals(t, principalAdapter(prPrincipal+"    GET \"/x\" -> takesString(me)\n        produces \"application/json\"\n"),
		"workflow takesString(me: string) -> string {\n    return me\n}")
	wantOnly(t, got, "U4", "principal me is Identity, but takesString's parameter me is string")
}

// ADR 0141: a principal declared once at the top level, named by adapters.
const prTopMe = "principal me: Identity from cookie \"sid\" by signedIn else signInFirst"

func TestANamedPrincipalChecks(t *testing.T) {
	got := checkPrincipals(t, principalAdapter("    principal me\n"+
		"    GET \"/mine\" -> showMine(me)\n        produces \"application/json\"\n"), prTopMe)
	if len(got) != 0 {
		t.Fatalf("diags = %v, want none", got)
	}
	// An inline principal may be settled from a named one.
	role := "workflow signedInRole(me: Identity) -> Result<Identity, IdentityError> {\n    return Ok(me)\n}"
	showAgain := "workflow showAgain(again: Identity) -> string {\n    return again.userId\n}"
	got = checkPrincipals(t, principalAdapter("    principal me as person\n"+
		"    principal again: Identity from person by signedInRole else signInFirst\n"+
		"    GET \"/mine\" -> showAgain(again)\n        produces \"application/json\"\n"), prTopMe, role, showAgain)
	if len(got) != 0 {
		t.Fatalf("inline from named: diags = %v, want none", got)
	}
}

func TestU7APrincipalReferenceNamesATopLevelPrincipal(t *testing.T) {
	wantOnly(t, checkPrincipals(t, principalAdapter("    principal signedIn\n"+
		"    GET \"/covers/{id}\" public -> showCover(id: string)\n        produces \"application/json\"\n")), "U7", "names a workflow")
}

func TestU8AnAdapterHasOnePrincipalOfEachName(t *testing.T) {
	got := checkPrincipals(t, principalAdapter("    principal me\n"+prPrincipal+
		"    GET \"/mine\" -> showMine(me)\n        produces \"application/json\"\n"), prTopMe)
	wantOnly(t, got, "U8", "two principals named me")
}

func TestU9ATopLevelPrincipalIsSettledFromAnotherWithNoCycle(t *testing.T) {
	role := "workflow signedInRole(me: Identity) -> Result<Identity, IdentityError> {\n    return Ok(me)\n}"
	got := checkPrincipals(t, principalAdapter("    GET \"/covers/{id}\" -> showCover(id: string)\n        produces \"application/json\"\n"),
		"principal a: Identity from b by signedInRole else signInFirst",
		"principal b: Identity from a by signedInRole else signInFirst", role)
	if len(got) != 2 || !strings.Contains(got[0], "[U9]") || !strings.Contains(got[1], "settled from itself") {
		t.Fatalf("a cycle: %v", got)
	}
	wantOnly(t, checkPrincipals(t, principalAdapter("    GET \"/covers/{id}\" -> showCover(id: string)\n        produces \"application/json\"\n"),
		"principal a: Identity from signedIn by signedInRole else signInFirst", role), "U9", "isn't a top-level principal")
}
