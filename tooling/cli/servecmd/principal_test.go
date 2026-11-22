package servecmd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// ADR 0141 across packages: sessions declares `me` and `admin` once; books
// names `sessions.me` and admin names only `sessions.admin`, which settles
// `me` first. Each binder runs in sessions, where its names resolve.
var namedPrincipalApp = map[string]string{
	"app/fiig.pkg.json": `{"path":"p141","kind":"application","version":"0.1.0"}
`,
	"app/src/P141.fiig": `use p141.books
use p141.admin

application P141 {
    serve {
        books.booksApi
        admin.adminApi
    }
}
`,
	"app/src/admin/AdminApi.fiig": `use p141.sessions

adapter http adminApi {
    wire sessions.admin

    GET "/admin/stats" -> stats(admin)
        produces "application/json"
    {
        s -> 200 s
    }
}
`,
	"app/src/admin/Stats.fiig": `use p141.sessions

workflow stats(admin: sessions.AdminUser) -> string {
    return "stats for ${admin.userId}"
}
`,
	"app/src/books/BooksApi.fiig": `use p141.sessions

adapter http booksApi {
    wire sessions.me

    GET "/books" -> myBooks(me)
        produces "application/json"
    {
        books -> 200 books
    }
}
`,
	"app/src/books/MyBooks.fiig": `use p141.sessions

workflow myBooks(me: sessions.Identity) -> string {
    return "books of ${me.userId}"
}
`,
	"app/src/sessions/Admin.fiig": `wire admin: AdminUser from me by signedInAdmin else refused
`,
	"app/src/sessions/AdminUser.fiig": `model AdminUser {
    userId: string
}
`,
	"app/src/sessions/CheckAdmin.fiig": `activity checkAdmin(me: Identity) -> Result<AdminUser, IdentityError> {
    if not me.admin {
        return Err(IdentityError.NotAnAdmin)
    }
    return Ok(AdminUser(userId: me.userId))
}
`,
	"app/src/sessions/Identity.fiig": `model Identity {
    userId: string
    admin: bool
}
`,
	"app/src/sessions/IdentityError.fiig": `enum IdentityError {
    NotSignedIn
    NotAnAdmin
}
`,
	"app/src/sessions/LookUp.fiig": `activity lookUp(token: string | None) -> Result<Identity, IdentityError> {
    match token {
        None -> {
            return Err(IdentityError.NotSignedIn)
        }
        given -> {
            return Ok(Identity(userId: given, admin: given == "boss"))
        }
    }
}
`,
	"app/src/sessions/Me.fiig": `wire me: Identity from cookie "sid" by signedIn else refused
`,
	"app/src/sessions/Refused.fiig": `conversion refused(e: IdentityError) -> HttpResponse<string> {
    match e {
        IdentityError.NotSignedIn -> {
            return HttpResponse(status: 401, body: "sign in first")
        }
        IdentityError.NotAnAdmin -> {
            return HttpResponse(status: 403, body: "admins only")
        }
    }
}
`,
	"app/src/sessions/SignedIn.fiig": `workflow signedIn(token: string | None) -> Result<Identity, IdentityError> {
    return lookUp(token)
}
`,
	"app/src/sessions/SignedInAdmin.fiig": `workflow signedInAdmin(me: Identity) -> Result<AdminUser, IdentityError> {
    return checkAdmin(me)
}
`,
}

func TestANamedPrincipalIsSettledInItsOwnPackage(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, namedPrincipalApp)
	env, _ := runprogram.NewEnvironment("")
	handler, _, _, caps, err := load(filepath.Join(root, "app"), nil, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(caps.Close)
	cases := []struct {
		path, cookie string
		code         int
		want         string
	}{
		{"/books", "", 401, "sign in first"},
		{"/books", "ann", 200, "books of ann"},
		{"/admin/stats", "", 401, "sign in first"},
		{"/admin/stats", "ann", 403, "admins only"},
		{"/admin/stats", "boss", 200, "stats for boss"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "sid", Value: c.cookie})
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s with %q: %d %s, want %d %s", c.path, c.cookie, rec.Code, rec.Body.String(), c.code, c.want)
		}
	}
}
