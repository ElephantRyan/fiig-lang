package runtime

import "testing"

// A name inside Ok(...) binds the payload as it is, even when the payload
// is itself a Result; only a bare name at the top of an arm unwraps one.
// Found by ADR 0084's relay driver, whose reply is a decoded nested Result.
func TestNameInsideOkKeepsANestedResult(t *testing.T) {
	src := `rule unwrapOnce(n: int) -> Result<int, string> {
    inner: Result<int, string> = Ok(n);
    outer: Result<Result<int, string>, string> = Ok(inner);
    return match (outer) {
        Ok(reply) -> {
            return reply;
        },
        Err(problem) -> {
            return Err(problem);
        },
    }
}`
	got, f := runEach(t, src, "unwrapOnce", map[string]Value{"n": Int(5)})
	if f != nil {
		t.Fatal(f)
	}
	if !Equal(got, Result{Ok: true, Value: Int(5)}) {
		t.Fatalf("got %v, want Ok(5)", got)
	}
}
