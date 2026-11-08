# Contract conformance

The OpenAPI document `api/openapi.json` is the contract, and the tests hold
the service to it in both directions. The design is on the wiki page
[Contract and Schema Testing](https://github.com/mavrovde/viafrei-api/wiki/Contract-and-Schema-Testing)
(TEST-003, TEST-004, TST-015, NFR-SEC-002, ERR-001, ERR-005).

The harness is [`internal/testkit/oascheck`](../internal/testkit/oascheck).

## Every response is validated

The handler tests' request helper (`do` in `internal/adapters/httpapi`)
passes every response it receives to `oascheck.Check`. A test fails when:

- the status is not documented for the operation (`default` documents error
  answers only, so a success status must be declared by itself), or a 2xx
  answer comes from a path the document does not hold;
- a header is not documented for that status. Two lists in the harness say,
  with a reason each, which headers need no per-operation declaration: the
  transport headers (`Content-Type`, `Date`, `X-Request-Id`, the security
  headers) and the problem headers (`Cache-Control`, `Retry-After`);
- the media type is not documented, or the body does not validate against the
  schema for that status. A removed required member and an extra member both
  fail, because response schemas set `additionalProperties: false`;
- a 4xx or 5xx answer is not `application/problem+json`, unless the document
  declares another media type for that exact status (readiness `503`).

The only routes left out are the two that serve the document itself.

## Requests in both directions

`TestContractInBothDirections` builds its requests from the document:

- **Negative**: one invalid request for each constraint of each query parameter
  (wrong type, below minimum, above maximum, at an exclusive bound, too short,
  too long, outside the enumeration, not matching the pattern, missing
  required, repeated), plus one unknown parameter per operation. Each must be
  answered `400 invalid-parameter`, with that parameter in `errors[]`.
- **Positive**: one request per operation from the documented parameter
  examples, answered with a documented status other than 400.

The harness fails closed: an empty or unparsable document, a missing Problem
schema, an operation with no id or no response, a required parameter with no
value it can derive, and a pattern no candidate value violates are each an
error. Every run prints how many requests it sent and how many were answered
as documented.

## Coverage

At the end of the handler tests, the operation × documented status table is
printed, with each pair marked when at least one valid response covered it:

```sh
go test -count=1 -v ./internal/adapters/httpapi/ | sed -n '/OpenAPI conformance/,$p'
```

## Against the last release

CI compares the generated document with the main branch
(`scripts/openapi-diff.sh`) and with the last released document
(`scripts/openapi-released.sh`, TEST-004). The second is the newest final
`vX.Y.Z` tag reachable from HEAD. A removed operation, member or enum value,
or a tightened parameter, fails. Before the first release there is nothing to
compare, and the job says so. A shallow clone is a refusal, never a pass.
