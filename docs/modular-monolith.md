# A modular monolith in fiig

An application that will grow usually starts as one service and splits
later. Until it splits, keep its domains (Orders, Payments, Customers)
separate enough that pulling one out is a small change rather than an
untangling. fiig doesn't enforce the boundary; it gives it an obvious
shape (ADR 0079). `examples/shop-orders` is a monolith built this way, and
`examples/shop-orders-split` is the same application after Payments moved
out.

## The shape

- **A workspace of packages:** a small shared kernel, one package per
  domain, and an application that depends on them. Each package has its
  own `fiig.pkg.json`, so what a domain may use is the dependencies it
  lists, and the loader refuses a cycle between domains.
- **A domain's behaviour is its workflows.** Another domain reaches them
  only through a gateway that names the domain in its `requires` line:

  ```fiig
  use shop.kernel;
  use shop.payments;

  gateway chargeOrder(order: string, amount: kernel.Money) -> Result<Charge, ChargeError>
          requires payments {
      return match (payments.capturePayment(reference: order, amount: amount)) {
          Ok(receipt) -> Ok(Charge(id: receipt.id, refundable: receipt.refundable)),
          Err(failure) -> Err(chargeErrorOf(failure)),
      }
  }
  ```

  Only a gateway may call another package's workflow, and only the one
  package it requires (B4 otherwise; B8 from an activity or a workflow).
  Within a package, a workflow is still called only by an adapter.
- **The seam already looks remote.** The gateway returns a `Result`, so
  its callers handle failure from day one, and it can't join a
  transaction on your `Database` (C4), so no code depends on Orders and
  Payments committing together. When Payments leaves, none of that
  changes for the callers.
- **`grep "requires payments"`**, or `fiig catalog find --requires
  shop.payments`, lists every place that depends on Payments: the list to
  read before deciding it's ready to leave.

## Splitting a domain out

Change the gateways that require it, and nothing that calls them. The
split `chargeOrder` keeps its name and signature, and requires
`HttpClient` (`fiig.http`) instead:

```fiig
gateway chargeOrder(order: string, amount: kernel.Money) -> Result<Charge, ChargeError>
        requires HttpClient {
    // POST to Payments' route; decode a 200 as Charge and a 422 as ChargeError
}
```

Two habits make that hold:

- **A gateway returns its own domain's types.** `Charge` and `ChargeError`
  are Orders' models, converted from Payments' types inside the gateway.
  A gateway that returns `payments.Receipt` hands Payments' vocabulary to
  every caller, and the split reaches all of them.
- **A domain's adapter answers its error enum whole** (`Err(error) -> 422
  error`). ADR 0072's JSON form carries the variant and its fields, so the
  split gateway decodes the refusal straight into its own enum, when the
  variant names match.

`examples/shop-orders-split` differs from `examples/shop-orders` in the
gateway and in the deployment's own files: the manifest (it depends on
`fiig.http`, not `shop.payments`), the lockfile, the caps file, and a
config for Payments' URL. A test holds every other file byte for byte.

## What to share, and what not to

Not every use of another domain's code should be a gateway. fiig's kinds
already sort it:

| Kind | What it is | Across a domain boundary |
|---|---|---|
| `model`, `enum` | vocabulary | shared: a shared-kernel package every domain depends on (`Money`, `CustomerId`) |
| `formulas` group | pure building blocks | shared, the same way |
| `rule` | a business decision | owned by one domain; another asks it through a gateway, or gets the answer as data |
| `conversion` | translating between shapes | each side owns its own |
| `workflow` | what the domain does | called through a gateway that requires the domain |

A decision imported by a second domain is the smell. `isRefundable` in a
shared library still gives two answers once the services deploy
separately, since each pins its own version, and a copy drifts the same
way. So Payments owns it, and Orders reads `refundable` from the receipt.
The exception is a look-alike: two rules with the same body that answer
different questions are better kept as two, since they'll diverge. The
compiler can't tell which is which, so this is a habit, not a check.

## Serving the monolith

`fiig serve` takes every adapter the process serves: one in the root module's
folders (`orders.ordersApi`), or a dotted one from a dependency (its package
path, then its name):

```
fiig serve -workspace .. -caps caps.json . orders.ordersApi shop.payments.paymentsApi
```

`-workspace ..` says where the sibling packages are. To say it once, put
`"workspaces": [".."]` in the application's `fiig.pkg.json` (ADR 0097),
and every command finds them without the flag.

They share one listener, one set of request limits, and one CORS policy.
Two routes that would serve the same requests stop it before it starts,
so give each domain its own path prefix (`/payments/...`); its URLs then
don't change when it moves.

## A database per domain

A shared table outlives every boundary in the code. Give each domain its
own database, or its own schema, with the caps file's `packages` section:

```json
{
  "Database": {"type": "sql", "driver": "postgres", "dsn": "${ORDERS_DB}"},
  "packages": {
    "shop.payments": {"Database": {"type": "sql", "driver": "postgres", "dsn": "${PAYMENTS_DB}"}}
  }
}
```

A package without an entry uses the process-wide binding. A binding
follows the workflow that is running: a library's gateway that Payments
calls uses Payments' database. `fiig serve` at startup, and `fiig config`,
list where each domain's capabilities come from:

```
shop.payments: Database (its own binding)
```

or `(shared with the application)` when it falls back. When Payments
leaves, its entry becomes a plain `"Database"` in its own caps file.
