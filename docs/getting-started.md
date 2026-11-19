# Getting started

This builds and runs `examples/getting-started`, a small ratings service:
a model, a validation rule, two SQLite gateways, and an HTTP adapter. You
need the `fiig` binary and nothing else; SQLite is built in.

## 1. Install

Download the archive for your platform from the release page, unpack it,
and put `fiig` (`fiig.exe` on Windows) on your `PATH`. Check it:

```
fiig version
```

The archive includes this guide's example under `examples/getting-started`.
To build from source instead, with Go 1.26:

```
cd cmd/fiig
go build -o fiig .
```

## 2. Check the program

```
cd examples/getting-started
fiig check .
```

No output means every file type-checks. `fiig` refuses to run or serve a
program that doesn't.

## 3. Create the table and rate something

`caps.json` connects the `Database` capability to a SQLite file,
`ratings.db`, created on first use.

```
fiig run -caps caps.json . setup
fiig run -caps caps.json -args '{"input": {"item": "dune", "score": 9, "note": "great"}}' . rate
fiig run -caps caps.json -args '{"item": "dune"}' . readRating
```

The last command prints `Ok(Rating{item: dune, note: great, score: 9})`. A
score of 11 comes back as `Err(RatingError.Invalid(score, must be from 1
to 10))`, a value the program handles, not an exception.

On Windows PowerShell, quote the JSON with single quotes and double every
inner double quote, or put the arguments in a variable.

## 4. Serve it over HTTP

```
fiig serve -caps caps.json . ratingsApi
```

From another terminal:

```
curl -X POST localhost:8080/ratings -H 'Content-Type: application/json' -d '{"item": "hyperion", "score": 8, "note": "dense"}'
curl localhost:8080/ratings/hyperion
curl localhost:8080/ratings/nope
```

The first two return the rating with status 200; the last returns `"no
rating for nope"` with status 404, the mapping written in
`adapters/ratings_api.fiig`. The route declares `consumes
"application/json"`, so a POST without that `Content-Type` is a 415.

## 5. See an error

In `activities/record_rating.fiig`, delete the `?` after
`createValidRating(input)`, then check again:

```
fiig check .
```

```
activities/record_rating.fiig:4:26: [A1] type mismatch: got Result<Rating, RatingError>, expected Rating
```

`createValidRating` can fail, and the program no longer says what happens
when it does, so it doesn't compile. Every diagnostic carries a bug-class
code; `docs/specification/bug-classes.md` lists them, and
`examples/invalid-programs` holds a program for each. Put the `?` back.

## 6. Test it

`tests/` holds tests written in fiig. Each one gets its own empty
in-memory database, so the gateways run their real SQL:

```
fiig test -v .
```

```
ok   badBatchStoresNothing (tests/bad_batch_stores_nothing.fiig:3)
ok   ratingAboveTenIsRefused (tests/rating_above_ten_is_refused.fiig:2)
ok   savedRatingIsFound (tests/saved_rating_is_found.fiig:3)
3 passed, 0 failed
```

`expect found == ...` checks a value; `expect result is Err(problem)`
checks a shape and binds what's inside. Change `"great"` to `"grand"` in
`tests/saved_rating_is_found.fiig` and run it again to see a failure
with both sides printed. ADR 0091 has the rest, including `fake` blocks
that stand in for a capability.

## Next

- `docs/specification/tour.md`: the language in one pass.
- `docs/deploying.md`: running a program as a service or a container.
- `docs/modular-monolith.md`: several domains in one application.
- `docs/known-limitations.md`: what this alpha does not do yet.
