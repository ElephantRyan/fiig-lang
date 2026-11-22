package adapter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR 0080 §3: an enum read from a query value, a header, and a cookie,
// by its spelling, and None when an optional one is missing.
var textFormProgram = []string{
	`enum Speed {
    Standard from json "standard"
    Express from json "express"
}`,
	`workflow describe(speed: Speed | None) -> string {
    return match (speed) {
        None -> {
            return "none";
        }
        Speed.Standard -> {
            return "standard";
        }
        Speed.Express -> {
            return "express";
        }
    }
}`,
	`adapter http speeds {
    GET "/by-query?speed={speed}" -> describe(speed: Speed | None)
        produces "application/json"
    GET "/by-header" -> describe(speed: Speed | None from header "X-Speed")
        produces "application/json"
    GET "/by-cookie" -> describe(speed: Speed | None from cookie "speed")
        produces "application/json"
}`,
}

func TestEnumsReadFromText(t *testing.T) {
	h := handlerFor(t, textFormProgram...)
	get := func(path string, set func(*http.Request)) string {
		r := httptest.NewRequest("GET", path, nil)
		if set != nil {
			set(r)
		}
		code, _, body := serve(h, r)
		return fmt.Sprintf("%d %s", code, strings.TrimSpace(body))
	}
	cases := []struct {
		path string
		set  func(*http.Request)
		want string
	}{
		{"/by-query?speed=express", nil, `200 "express"`},
		{"/by-query", nil, `200 "none"`},
		{"/by-query?speed=Express", nil, `400 {"error":"query value \"speed\": \"Express\" isn't a Speed; expected standard or express"}`},
		{"/by-header", func(r *http.Request) { r.Header.Set("X-Speed", "standard") }, `200 "standard"`},
		{"/by-header", nil, `200 "none"`},
		{"/by-cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "speed", Value: "express"}) }, `200 "express"`},
	}
	for _, c := range cases {
		if got := get(c.path, c.set); got != c.want {
			t.Errorf("GET %s: got %s, want %s", c.path, got, c.want)
		}
	}
}
