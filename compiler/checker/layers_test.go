package checker

import "testing"

const layersGateway = `gateway findBook(id: string) -> string
    requires Database
{
    return id;
}`

const layersActivity = `activity loadBook(id: string) -> string {
    return findBook(id);
}`

// ADR 0107: a workflow reaches a gateway through an activity.
func TestWorkflowMayNotCallAGateway(t *testing.T) {
	diags := checkSrc(t, "workflow rate(id: string) -> string {\n    return findBook(id);\n}", layersGateway)
	wantDiag(t, diags, "B12", `workflow may not call gateway "findBook"; call it from an activity`)
	diags = checkSrc(t, "workflow rate(id: string) -> string {\n    return loadBook(id);\n}", layersGateway, layersActivity)
	if len(diags) != 0 {
		t.Fatalf("through an activity: diags = %v, want none", diags)
	}
}

// ADR 0103: a test may call its package's workflow. (Running one is
// examples/getting-started/tests/rate_then_read.fiig.)
func TestATestMayCallAWorkflow(t *testing.T) {
	sources := map[string]string{
		"gateways/find_book.fiig":   layersGateway + "\n",
		"activities/load_book.fiig": layersActivity + "\n",
		"workflows/rate.fiig":       "workflow rate(id: string) -> string {\n    return loadBook(id);\n}\n",
		"tests/rate.fiig":           "test rateReadsTheBook {\n    expect rate(\"b-1\") == \"b-1\";\n}\n",
	}
	diags, _ := CheckSources(sources, nil, "", nil)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// ADR 0139 §2 (Hearsay G58): a rule calling a rule is told to take the
// other rule's answer as a parameter, first.
func TestARuleCallingARuleIsToldToTakeItsAnswer(t *testing.T) {
	diags := checkSrc(t,
		"rule syncTargets(userType: string) -> int {\n    if (canAdminister(userType)) {\n        return 2;\n    }\n    return 1;\n}",
		"rule canAdminister(userType: string) -> bool {\n    return userType == \"admin\";\n}")
	wantDiag(t, diags, "B7", "a rule doesn't combine decisions, so take its answer as a parameter (`administer: bool`) and call canAdminister from the activity that calls syncTargets (ADR 0029, ADR 0139). If syncTargets only reshapes data, it can be a conversion")
}

func TestAnswerParamName(t *testing.T) {
	for in, want := range map[string]string{"client.canAdminister": "administer", "isOpen": "open", "hasRole": "role", "island": "island", "can": "can", "score": "score"} {
		if got := answerParamName(in); got != want {
			t.Errorf("answerParamName(%q) = %q, want %q", in, got, want)
		}
	}
}
