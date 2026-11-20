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
    return mapErr(e in AbsApi.item(id)): appErrorOfAbs(e)
}
```

**Activity.** A step several features need: who is asking, and which book.
It knows nothing about why it was called:

```fiig
activity loadUserAndBook(session: string | None, itemId: string) -> Result<UserAndBook, AppError> {
    me = currentUser(session)?
    book = findBook(itemId)?
    return Ok(UserAndBook(user: me, book: book))
}
```

**Workflow.** What the step's result means for *this* feature. Rating a
book that doesn't exist is, for this app, the same answer as rating one you
can't see:

```fiig
workflow rate(session: string | None, itemId: string, input: RatingInput) -> Result<Rating, AppError> {
    match loadUserAndBook(session, itemId) {
        Err(AppError.NotFound) -> return Err(AppError.Unauthorized)
        Err(other) -> return Err(other)
        Ok(found) -> return saveRating(found, input)
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
        Ok(rating) -> 200 rating
        Err(error) -> appErrorResponse(error)
    }
}
```

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
