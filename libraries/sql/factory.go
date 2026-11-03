package sql

import (
	"database/sql"
	"fmt"

	"fiig-lang/compiler/runtime"
)

// Factory is a runtime.ProviderFactory — the declarative-config half of
// this library (ADR 0025's `caps.Register("Database", sql.New(db))`
// wiring is still exactly what this compiles down to; a host just doesn't
// have to write that Go line by hand anymore). Config shape:
//
//	{"type": "sql", "driver": "sqlite", "dsn": "file::memory:?cache=shared"}
//
// "driver" is whatever name a database/sql driver registered itself under
// (via its own `init`'s sql.Register, or a blank import the host adds) —
// this package still has no opinion about which one and imports none
// itself, same as Provider/New. That's the whole point of going through
// database/sql at all (ADR 0025's survey of JDBC/DB-API/etc.): the driver
// choice stays entirely with whoever deploys the application.
func Factory(config map[string]any) (runtime.CapabilityProvider, error) {
	driver, ok := config["driver"].(string)
	if !ok || driver == "" {
		return nil, fmt.Errorf(`sql.Factory: config has no "driver"`)
	}
	dsn, ok := config["dsn"].(string)
	if !ok || dsn == "" {
		return nil, fmt.Errorf(`sql.Factory: config has no "dsn"`)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Factory: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("sql.Factory: %w", err)
	}
	return New(db), nil
}
