# Go vs JVM Spike

See `docs/decisions/0001-go-vs-jvm-spike-plan.md` for the plan and scoring table.

## 1. Cold start

```bash
go build -o go/cli/cli.exe ./go/cli
javac -d java java/Cli.java

./scripts/bench_coldstart.sh ./go/cli/cli.exe
./scripts/bench_coldstart.sh java -cp java Cli
```

## 2. Bounded fan-out (10,000 items, concurrency 50)

```bash
go run ./go/fanout

java --enable-preview java/Fanout.java   # JDK 21; drop --enable-preview on 22+ if unneeded
```

## 3. HTTP under load

Terminal A (Go server):
```bash
go run ./go/httpserver
```

Terminal A (Java server, instead):
```bash
java java/HttpServerApp.java
```

Terminal B (load generator, same tool against either server):
```bash
go run ./go/loadgen -duration 60s -concurrency 50
```

For GC pause data on the JVM run, add: `java -Xlog:gc -cp java HttpServerApp`

## 4. Cross-compile

```bash
GOOS=linux GOARCH=amd64 go build -o dist/cli-linux ./go/cli
GOOS=darwin GOARCH=arm64 go build -o dist/cli-mac ./go/cli
GOOS=windows GOARCH=amd64 go build -o dist/cli.exe ./go/cli
```

Java equivalent is a single portable jar (`jlink`/`jpackage` only needed if bundling a JRE) — note the extra step count and artifact size in the table.

Record all results in the table in `0001-go-vs-jvm-spike-plan.md`.
