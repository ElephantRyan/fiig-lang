// Package servecmd is `fiig serve`: it checks a fiig program and serves one of its `adapter
// http` declarations: the deployable form of a fiig application. It is
// `fiig run`'s counterpart for a long-running service, with the same
// capability wiring (ADR 0027): -caps names a JSON file whose "sql"-typed
// capability opens a real database (when the binary registers the sql
// factory, as `fiig` does), whose "socket"-typed capability is the real
// network, and whose strings may name environment variables as `${NAME}`,
// so a container passes its paths and secrets in the environment:
//
//	{"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:${DB_PATH}"}}
//
// A program with checker diagnostics is refused, as fiig-sql-run refuses
// one. SIGINT or SIGTERM stops accepting connections and lets in-flight
// requests finish for up to -shutdown.
//
// Usage:
//
//	fiig serve [-addr :8080] [-caps caps.json] [-workspace <dir>]... <root> <adapterName>
package servecmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/compiler/version"
	"fiig-lang/tooling/runprogram"
)

type stringList []string

func (s *stringList) String() string     { return fmt.Sprint(*s) }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// Main serves; factories are the provider types the host adds to the
// built-in ones, such as libraries/sql's "sql".
func Main(name string, args []string, factories runtime.FactoryRegistry) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addr := fs.String("addr", envOr("LISTEN_ADDR", ":8080"), "address to listen on (default $LISTEN_ADDR, else :8080)")
	capsPath := fs.String("caps", "", "path to a JSON file of capability configuration (ADR 0027); `${NAME}` in its strings reads the environment")
	shutdown := fs.Duration("shutdown", 10*time.Second, "how long in-flight requests may run after SIGINT/SIGTERM")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable")
	var origins stringList
	fs.Var(&origins, "allow-origin", "an origin a browser front end may call from, as scheme://host[:port], or * for any (ADR 0070); repeatable; default $ALLOW_ORIGINS, comma-separated; none means CORS off")
	fs.Parse(args)
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-addr ADDR] [-caps FILE] [-allow-origin ORIGIN]... [-workspace DIR]... <root> <adapterName>")
		os.Exit(2)
	}
	if len(origins) == 0 {
		origins = strings.Split(os.Getenv("ALLOW_ORIGINS"), ",")
	}
	cors, err := adapter.ParseOrigins(origins)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -allow-origin:", err)
		os.Exit(1)
	}
	handler, err := load(fs.Arg(0), fs.Arg(1), *capsPath, workspaces, factories, cors)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		done, cancel := context.WithTimeout(context.Background(), *shutdown)
		defer cancel()
		srv.Shutdown(done)
	}()
	log.Printf("fiig %s: serving %s on %s", version.Version, fs.Arg(1), *addr)
	if cors != nil {
		log.Printf("fiig: allowing cross-origin calls from %s", strings.Join(cors.Origins(), ", "))
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	log.Print("fiig: stopped")
}

// load checks root and builds the handler for its http adapter named name.
func load(root, name, capsPath string, workspaces []string, factories runtime.FactoryRegistry, cors *adapter.CORS) (http.Handler, error) {
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		return nil, err
	}
	var packages types.Packages
	if len(workspaces) > 0 {
		loaded, loadErrs := loader.Load(root, workspaces)
		if len(loadErrs) > 0 {
			return nil, errors.Join(loadErrs...)
		}
		packages = loaded.Packages
	}
	diags = append(diags, runprogram.Check(root, files, packages)...)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		return nil, fmt.Errorf("refusing to serve a program with checker diagnostics")
	}
	universe, _ := types.BuildUniverse(files) // conflicts already reported by runprogram.Check above

	var decl *syntax.AdapterDecl
	for _, f := range files {
		for _, d := range f.Decls {
			if ad, ok := d.(*syntax.AdapterDecl); ok && ad.Name == name {
				decl = ad
			}
		}
	}
	if decl == nil {
		return nil, fmt.Errorf("no adapter named %q", name)
	}

	caps, err := runprogram.LoadCapabilitiesFile(capsPath, factories)
	if err != nil {
		return nil, fmt.Errorf("-caps: %w", err)
	}
	if err := runprogram.RefuseSeededRandom(caps); err != nil {
		return nil, err
	}
	runprogram.RegisterDefaults(caps)
	in := runtime.New(universe, caps)
	in.Imports = runprogram.BuildImports(files, packages)
	in.Groups = runprogram.BuildGroups(files, packages)
	h, err := adapter.HTTPHandler(decl, universe, in)
	if err != nil {
		return nil, err
	}
	return adapter.WithCORS(h, decl, cors)
}

func envOr(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return fallback
}
