package checker

import "testing"

const (
	tryIdentity = "enum IdentityError {\n    NotSignedIn\n}"
	tryRating   = "enum RatingError {\n    NotSignedIn\n    NoRating\n}"
	tryStep     = "rule currentUser(token: string) -> Result<string, IdentityError> {\n    return Err(IdentityError.NotSignedIn)\n}"
)

// F2 (hearsay GAPS G56): `?` returns its Err unchanged (ADR 0006), so an Err
// of another type would leave the declaration with a value its type doesn't
// admit, and a caller's exhaustive match would have no arm for it.
func TestTryReturnsOnlyTheDeclarationsErrorF2(t *testing.T) {
	diags := checkSrc(t, "conversion rate(token: string) -> Result<string, RatingError> {\n    me = currentUser(token)?\n    return Ok(me)\n}", tryIdentity, tryRating, tryStep)
	wantDiag(t, diags, "F2", "the Err here is IdentityError, but the declaration's error is RatingError")

	diags = checkSrc(t, "conversion rate(token: string) -> Result<string, RatingError> {\n    me = mapErr(e in currentUser(token)): RatingError.NotSignedIn?\n    return Ok(me)\n}", tryIdentity, tryRating, tryStep)
	if len(diags) != 0 {
		t.Fatalf("converted with mapErr: diags = %v, want none", diags)
	}

	diags = checkSrc(t, "conversion rate(token: string) -> Result<string, RatingError> {\n    me = mapErr(e in currentUser(token)): IdentityError.NotSignedIn?\n    return Ok(me)\n}", tryIdentity, tryRating, tryStep)
	wantDiag(t, diags, "F2", "the Err here is IdentityError")

	diags = checkSrc(t, "conversion rate(token: string) -> string {\n    me = currentUser(token)?\n    return me\n}", tryIdentity, tryRating, tryStep)
	wantDiag(t, diags, "F2", "not a Result")
}

func TestTryWithTheSameErrorIsFine(t *testing.T) {
	diags := checkSrc(t, "conversion check(token: string) -> Result<int, IdentityError> {\n    me = currentUser(token)?\n    return Ok(me.length())\n}", tryIdentity, tryRating, tryStep)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}
