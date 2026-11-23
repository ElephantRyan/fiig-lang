package checker

import "testing"

// ADR 0080: an enum's text form, and W4.

var textFormSources = []string{
	`enum Speed {
    Standard from json "standard",
    Express from json "express"
}`,
	`enum Destination {
    Domestic(postcode: string),
    International(country: string, postcode: string)
}`,
	`model Parcel {
    grams: int;
}`,
	`workflow rate(speed: Speed, weight: int) -> int {
    return weight;
}`,
	`workflow maybeRate(speed: Speed | None) -> int {
    return 1;
}`,
	`workflow ship(destination: Destination) -> int {
    return 1;
}`,
	`workflow weigh(parcel: Parcel) -> int {
    return parcel.grams;
}`,
}

func withTextForm(adapter string) []string {
	return append(append([]string{}, textFormSources...), adapter)
}

// §3: a payload-free enum binds from a path, a query value, a header, a
// cookie, and the command line, optionally | None.
func TestPayloadFreeEnumsBindFromText(t *testing.T) {
	for _, adapter := range []string{
		"adapter http a {\n    GET \"/rates/{speed}?weight={weight}\" -> rate(speed: Speed, weight: int)\n        produces \"application/json\";\n}",
		"adapter http a {\n    GET \"/rates?speed={speed}\" -> maybeRate(speed: Speed | None)\n        produces \"application/json\";\n}",
		"adapter http a {\n    GET \"/rates\" -> maybeRate(speed: Speed | None from header \"X-Speed\")\n        produces \"application/json\";\n}",
		"adapter http a {\n    GET \"/rates\" -> maybeRate(speed: Speed | None from cookie \"speed\")\n        produces \"application/json\";\n}",
		"adapter cli a {\n    command \"rate {speed} {weight}\" -> rate(speed: Speed, weight: int);\n}",
	} {
		if diags := checkSrc(t, withTextForm(adapter)...); len(diags) != 0 {
			t.Errorf("%s\nunexpected diagnostics: %+v", adapter, diags)
		}
	}
}

// §6: W4, at the argument, for an enum with payloads and for anything
// else text can't be read as, wherever the text comes from.
func TestW4ReadFromText(t *testing.T) {
	cases := []struct{ adapter, fragment string }{
		{"adapter http a {\n    GET \"/ship/{destination}\" -> ship(destination: Destination)\n        produces \"application/json\";\n}",
			`"destination" is read from a path placeholder, but Destination has variants with payloads, which no single text can carry`},
		{"adapter http a {\n    GET \"/ship?destination={destination}\" -> ship(destination: Destination)\n        produces \"application/json\";\n}",
			`"destination" is read from a query placeholder, but Destination has variants with payloads`},
		{"adapter http a {\n    GET \"/weigh/{parcel}\" -> weigh(parcel: Parcel)\n        produces \"application/json\";\n}",
			`"parcel" is read from a path placeholder, so it must be string, int, float, decimal, bool, or an enum without payloads, optionally | None; got Parcel`},
		{"adapter http a {\n    GET \"/ship\" -> ship(destination: Destination from header \"X-Destination\")\n        produces \"application/json\";\n}",
			`"destination" is read from a header, but Destination has variants with payloads`},
		{"adapter cli a {\n    command \"weigh {parcel}\" -> weigh(parcel: Parcel);\n}",
			`"parcel" is read from the command line, so it must be`},
	}
	for _, c := range cases {
		diags := checkSrc(t, withTextForm(c.adapter)...)
		wantDiag(t, diags, "W4", c.fragment)
	}
}

// §1: J2 for an enum, two variants with one spelling, including a
// spelling that is another variant's own name.
func TestJ2OnVariantSpellings(t *testing.T) {
	for _, src := range []string{
		"enum State {\n    Sent from json \"sent\",\n    Delivered from json \"sent\"\n}",
		"enum State {\n    Sent from json \"Delivered\",\n    Delivered\n}",
	} {
		wantDiag(t, checkSrc(t, src), "J2", "already is (ADR 0080 §1)")
	}
	if diags := checkSrc(t, "enum State {\n    Sent from json \"sent\",\n    Delivered from json \"delivered\"\n}"); len(diags) != 0 {
		t.Errorf("distinct spellings: unexpected diagnostics %+v", diags)
	}
}
