package checker

import "testing"

// ADR 0095: mapErr(e in result): newError.

const mapErrDecls = `enum ApiError {
    Unreadable(failure: JsonError)
    Refused(reason: string)
}`

const mapErrUser = `model User {
    name: string
}`

func TestMapErrChecks(t *testing.T) {
	diags := checkSrc(t, `gateway readUser(body: bytes) -> Result<User, ApiError>
    requires Database
{
    user: User = mapErr(e in json.decode(body)): ApiError.Unreadable(failure: e)?;
    return Ok(user);
}`, mapErrDecls, mapErrUser)
	if len(diags) != 0 {
		t.Fatalf("decode through ?: diags = %v, want none", diags)
	}
	diags = checkSrc(t, `gateway readUser(body: bytes) -> Result<User, ApiError>
    requires Database
{
    return mapErr(e in json.decode(body)): ApiError.Unreadable(failure: e);
}`, mapErrDecls, mapErrUser)
	if len(diags) != 0 {
		t.Fatalf("decode returned: diags = %v, want none", diags)
	}
	// §3: a body in a gateway may call a conversion.
	diags = checkSrc(t, `gateway readUser(body: bytes) -> Result<User, ApiError>
    requires Database
{
    return mapErr(e in json.decode(body)): refusal(e);
}`, `conversion refusal(e: JsonError) -> ApiError {
    return ApiError.Refused(reason: "unreadable");
}`, mapErrDecls, mapErrUser)
	if len(diags) != 0 {
		t.Fatalf("conversion in a body: diags = %v, want none", diags)
	}
}

func TestMapErrMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		{`gateway readUser(n: int) -> Result<int, ApiError>
    requires Database
{
    return mapErr(e in n): ApiError.Refused(reason: "x");
}`, "A1", "mapErr takes a Result, got int"},
		{`gateway readUser(body: bytes) -> Result<User, ApiError>
    requires Database
{
    return mapErr(e in json.decode(body)): "unreadable";
}`, "A1", "type mismatch"},
		{`gateway readUser(body: bytes) -> Result<User, ApiError>
    requires Database
{
    unread = mapErr(e in json.decode(body)): ApiError.Unreadable(failure: e);
    return Err(ApiError.Refused(reason: "x"));
}`, "A12", "json.decode has no type to decode into"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src, mapErrDecls, mapErrUser), c.rule, c.fragment)
	}
	// §3: no effects in a body, even in an activity, which may call gateways.
	diags := checkSrc(t, `activity readUser(body: bytes) -> Result<User, ApiError> {
    return mapErr(e in json.decode(body)): lookUp(e);
}`, `gateway lookUp(e: JsonError) -> ApiError
    requires Database
{
    return ApiError.Refused(reason: "x");
}`, mapErrDecls, mapErrUser)
	wantDiag(t, diags, "B5", `a mapErr body may not call gateway "lookUp"`)
}
