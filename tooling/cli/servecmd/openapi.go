package servecmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/runtime"
	"fiig-lang/tooling/runprogram"
)

// OpenAPIMain is `fiig openapi`: the OpenAPI 3.0.3 document for what
// `fiig serve` would serve, given the same root and adapter names, printed
// as JSON. Several adapters, a dependency's by its package path, are one
// document, as they're one listener (ADR 0079 §6). It reads no
// capabilities and no environment: the document is the program's.
//
// Usage:
//
//	fiig openapi [-workspace DIR]... <root> <adapterName>...
func OpenAPIMain(name string, args []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable")
	fs.Parse(args)
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-workspace DIR]... <root> <adapterName>...")
		os.Exit(2)
	}
	if err := writeOpenAPI(os.Stdout, fs.Arg(0), fs.Args()[1:], workspaces); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// writeOpenAPI checks root and writes the document for the adapters
// names names, indented.
func writeOpenAPI(w io.Writer, root string, names, workspaces []string) error {
	p, err := check(root, names, workspaces, "describe")
	if err != nil {
		return err
	}
	// The interpreter only resolves names: each declaration's types in
	// its own package and file, as the handler does.
	in := runtime.New(p.universe, runtime.NewCapabilityRegistry())
	in.Imports = runprogram.BuildImports(p.files, p.packages)
	in.Groups = runprogram.BuildGroups(p.files, p.packages)
	in.Packages = p.packages
	doc, err := adapter.GenerateOpenAPIFor(p.served, p.universe, in)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", out)
	return err
}
