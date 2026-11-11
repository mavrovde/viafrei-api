# Examples

Every operation in the [API reference](reference.html) and on the
[endpoints](endpoints.html) page shows a request and the answer the service
gives to it. These examples are not written by hand: they are recorded from
the service itself, running on **synthetic fixtures**, and they are checked
again on every change (DX-003).

## Where they come from

- One file per operation in `api/examples/`, named after its `operationId`.
  Each holds a title, the request (`GET` target), the key it is asked with
  (plan, scopes and source families, or none for an open operation), the
  attribution rule, the expected status and the answer.
- The answers are made from the generated fixture set (profile `tiny`, seed
  `1`). Every publisher, licence, place and value in them is **fictional**;
  source families are named `src-alpha-…` to `src-eta-…` and every URL ends
  in `.example`. No example shows a real station, price or count.
- `make generate` embeds them into `api/openapi.json`: as the response
  example (`examples.example`), as a `curl` code sample (`x-codeSamples`),
  as the request (`x-example-request`) and as the attribution rule
  (`x-attribution`).

## How they are checked

| Check | Where it runs | What fails it |
|---|---|---|
| Every example names an operation of the document, with its documented status | unit tests | an example for an operation that no longer exists or answers another status |
| Docs lint | unit tests | an operation without a description, a `curl` sample, an attribution rule or a success example; a keyed data answer whose example carries no `meta.attribution` with publisher, licence and text |
| Examples run | integration tests | an operation without an example; an answer that differs from the recorded one; an answer that does not validate against the document |
| Generated files | `make check-generated` | `api/openapi.json` not regenerated after an example changed |

So an example cannot go stale: changing an answer without re-recording its
example fails the integration tests, and adding an operation without one
fails both the docs lint and the example run.

## Recording after a change

```sh
make docs-examples RECORD=1   # re-record every answer from the fixtures
make generate                 # embed them into api/openapi.json
make docs-examples            # the same run, now as a check
make docs                     # build the portal into site/
```

Review the diff of `api/examples/` like code: it is exactly what users will
read. A new operation needs a new file in `api/examples/` (copy one of the
same kind, set the request and key, then record).
