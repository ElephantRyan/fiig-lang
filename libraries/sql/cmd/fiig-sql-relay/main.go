// fiig-sql-relay serves examples/sql-relay's protocol over SQLite, for
// trying a database driver written in fiig (ADR 0084) without a Postgres
// server:
//
//	go run ./libraries/sql/cmd/fiig-sql-relay -listen 127.0.0.1:5433 -db file:relay.db
package main

import (
	"database/sql"
	"flag"
	"log"
	"net"

	_ "modernc.org/sqlite"

	"fiig-lang/libraries/sql/sqlrelay"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5433", "address to listen on")
	dsn := flag.String("db", "file:relay.db", "SQLite database")
	flag.Parse()
	db, err := sql.Open("sqlite", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fiig-sql-relay: %s on %s", *dsn, ln.Addr())
	log.Fatal((&sqlrelay.Server{DB: db}).Serve(ln))
}
