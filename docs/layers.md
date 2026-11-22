# Layers: adapter, workflow, activity, gateway

A request passes through four layers, each with one job. The checker keeps
the calls in order (the composition matrix in the
[tour](specification/tour.md)); this page says what belongs in each.

| Layer | Its job | Its errors |
|---|---|---|
| **adapter** | turns a request into a workflow call, and the result into a response | the app's errors become HTTP statuses |
| **workflow** | one feature: decides what each step's result means here | reinterprets a step's error for this feature |
| **activity** | a step: coordinates gateways, often shared by several workflows | passes the app's errors on |
| **gateway** | talks to the outside world | the outside world's failures become the app's errors |

Rules and conversions are pure, and any layer may call them.

## An example: rating a book

**Gateway.** ABS's failures become the app's errors, once, here:

```fiig
gateway findBook(id: string) -> Result<Book, AppError>
    requires AbsApi
{
    return mapErr(e in AbsApi.item(id)): appErrorOfAbs(e);
}
```

**Activity.** A step several features need: who is asking, and which book.
It knows nothing about why it was called:

```fiig
activity loadUserAndBook(session: string | None, itemId: string) -> Result<UserAndBook, AppError> {
    me = currentUser(session)?;
    book = findBook(itemId)?;
    return Ok(UserAndBook(user: me, book: book));
}
```

**Workflow.** What the step's result means for *this* feature. Rating a
book that doesn't exist is, for this app, the same answer as rating one you
can't see:

```fiig
workflow rate(session: string | None, itemId: string, input: RatingInput) -> Result<Rating, AppError> {
    return match (loadUserAndBook(session, itemId)) {
        Err(AppError.NotFound) -> Err(AppError.Unauthorized),
        Err(other) -> Err(other),
        Ok(found) -> saveRating(found, input),
    }
}
```

**Adapter.** The app's errors become HTTP's:

```fiig
adapter http api {
    PUT "/ratings/{itemId}" -> rate(session: string | None from cookie "sid", itemId: string, input: RatingInput)
        consumes "application/json"
        produces "application/json"
    {
        Ok(rating) -> 200 rating,
        Err(error) -> appErrorResponse(error),
    }
}
```

## The order of checks

A request is refused at the first point that has the information (charter
principle 11):

1. **Who is asking.** The adapter settles its principal before it reads the
   body (ADR 0137), and a workflow that needs a caller takes one as a
   parameter, as below:

   ```fiig
   // src/sessions/Me.fiig: how a caller is recognised, written once
   wire me: accounts.Identity from cookie "sid" by signedIn else identityErrorResponse

   // src/ratings/RatingsApi.fiig: which wire guards these routes
   adapter http ratingsApi {
       wire sessions.me

       PUT "/ratings/{itemId}" -> putRating(me, itemId: string, input: RatingInput)
       ...
   }
   ```

   A wire shared by several adapters is declared once, in the package
   that owns the identity, and each adapter names it (ADR 0141, ADR 0143). One only
   a single adapter settles stays inline: `wire peer: LinkedServer
   from header "Authorization" by callingServer else peerRefused`.
2. **The workflow** runs its pure checks next: rules over the input, with no
   I/O.
3. **Then the steps.** Checks that need the outside world (does the book
   exist?) come first among them, and the writes come last.

A rule that both checks the input and builds what is stored often needs the
time or an id, which a workflow can't read (B10), and then the whole rule
ends up in the activity, after the I/O. Split it: a rule that checks the
input alone, called first in the workflow, and the building in the step.

```fiig
// Slow: a score of 12 costs an ABS round trip before it is refused, because
// the check is inside storeRating.
workflow putRating(me: Identity, itemId: string, input: RatingInput) -> Result<Rating, RatingError> {
    book = mapErr(e in latestBook(itemId)): RatingError.NoSuchBook?;
    return storeRating(me, book, input);
}

// Fast: the pure check first; the step takes what it returned.
workflow putRating(me: Identity, itemId: string, input: RatingInput) -> Result<Rating, RatingError> {
    valid = validRating(input)?;
    book = mapErr(e in latestBook(itemId)): RatingError.NoSuchBook?;
    return storeRating(me, book, valid);
}
```

The step taking `ValidRating` rather than `RatingInput` makes the right order
the obvious one: the raw input can't be handed to it.

## A step that reads two steps

Each step's error is in its own domain's terms, so an activity that reads
two steps with different errors has no single error type to pass up. Most of
the time that is a sign the two calls belong in the workflow, where each
error gets its meaning for the feature:

```fiig
workflow showMyListening(me: Identity) -> Result<Listening, ListeningError> {
    progress = mapErr(e in loadProgress(me.userId)): ListeningError.AbsDown?;
    books = mapErr(e in cachedBooks(progress.ids)): ListeningError.BooksUnavailable?;
    return Ok(Listening(progress, books));
}
```

When the activity really is one step, for example because both calls happen
in one transaction, give it an error enum whose variants carry each step's
error. `mapErr` wraps each one inline, with no conversion file:

```fiig
enum ListeningError {
    Progress(why: AbsFault)
    Books(why: BookError)
}

activity loadMyListening(userId: string) -> Result<Listening, ListeningError> {
    progress = mapErr(e in loadProgress(userId)): ListeningError.Progress(e)?;
    books = mapErr(e in cachedBooks(progress.ids)): ListeningError.Books(e)?;
    return Ok(Listening(progress, books));
}
```

Fiig has no union error types (`Result<T, AbsFault | BookError>`). `?` never
converts an error (ADR 0006, F2), and the named variants say more about what
failed (Hearsay GAPS G60).

## A rule that depends on another rule's answer

A rule doesn't call a rule (ADR 0029). When one decision depends on
another, such as "admin or self" depending on "is this person an admin",
the rule takes the other's answer as a parameter, and the activity that
calls it makes both decisions and passes the answer by name (ADR 0139):

```fiig
rule syncTargets(me: accounts.Identity, admin: bool, requested: string | None, everyone: List<string>) -> Result<List<string>, SyncError> { ... }

// in the activity
targets = syncTargets(me, admin: client.canAdminister(me.userType), requested: userId, everyone: all)?;
```

Its tests then say `admin: true` and `admin: false` directly, and
`canAdminister` is tested once, on its own. Don't make the caller a
conversion to get around this: a conversion reshapes data one level deeper,
and doesn't combine decisions. Don't make the callee a formula either,
unless it's a format check that any app would answer the same way (ADR
0049).

## Why a workflow doesn't call a gateway

A workflow that calls `findBook` directly depends on how the book is
found. With `loadUserAndBook` between them, it depends only on the step,
which can change, move, or be shared without touching the workflow. A
workflow calling a gateway is an error (B12, ADR 0107).

An activity that wraps a single gateway call isn't overhead: it's the
seam, there before you need it.

## Signs a layer is doing another's job

- **A workflow that is one line**, passing its arguments to one activity:
  its decisions have moved into the activity. Move them back.
- **An activity that matches on an error to change its meaning**: that's
  the workflow's decision.
- **A gateway that returns the outside world's error type**: the rest of
  the app now depends on it. Map it with `mapErr` (ADR 0095).

## Testing each layer

A test can call any of them (ADR 0103): a rule with values, a gateway
against the test's database or a fake, a workflow end to end. Test a
feature through its workflow; test a step through its activity when
several workflows share it.
