-- The table the gateways read and write. fiig applies this once, before
-- the program first runs, and records it in fiig_migrations (ADR 0096).
CREATE TABLE ratings (item TEXT PRIMARY KEY, score INTEGER NOT NULL, note TEXT NOT NULL);
