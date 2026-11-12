# Error codes

Every error is an [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem,
served as `application/problem+json`:

```json
{
  "type": "https://api.viafrei.de/problems/invalid-parameter",
  "title": "Invalid parameter",
  "status": 400,
  "detail": "One or more parameters are invalid.",
  "instance": "/v1/charging-options",
  "requestId": "example-request-0001",
  "retryable": false,
  "errors": [{ "parameter": "query.radius_m", "code": "invalid", "detail": "expected number <= 50000" }]
}
```

- `type` is stable for the life of `/v1`; branch on it, or on its last path
  segment (the code below), never on `title` or `detail`.
- `requestId` equals the `X-Request-Id` header. Quote it when you report a
  problem.
- `retryable` says whether repeating the same request may succeed. When
  `retryAfter` is present it equals the `Retry-After` header, in seconds.
- An answer never carries a stack trace, a query or an internal name.

*Planned:* each problem type URI will resolve to its section on this page,
whose anchor is the code; today the URIs are identifiers only.

The list below is the complete catalogue of this release, held equal to the
code by a test.

## Problem types

### invalid-parameter
Status `400` · Invalid parameter · not retryable.
A parameter is malformed, out of bounds, repeated or unknown. `errors` lists
one entry per violation.

### key-in-query
Status `400` · Key in query · not retryable.
A key was sent in the query string. It is refused before it is checked: treat
the key as exposed and have it rotated.

### missing-key
Status `401` · Missing key · not retryable.
The operation needs a key and none was presented. The answer carries
`WWW-Authenticate`.

### invalid-key
Status `401` · Invalid key · not retryable.
The key is malformed, unknown, wrong, or belongs to another environment. One
answer covers all of these on purpose.

### key-expired
Status `401` · Key expired · not retryable.
The key is past its expiry.

### key-revoked
Status `401` · Key revoked · not retryable.
The key was revoked.

### scope-denied
Status `403` · Scope denied · not retryable.
The key's plan does not include the operation's scope.

### source-not-permitted
Status `403` · Source not permitted · not retryable.
No source family that could answer is permitted to the key
([attribution and licence rules](attribution.md)).

### terms-not-accepted
Status `403` · Terms not accepted · not retryable.
Every family that could answer needs its terms accepted first.

### not-found
Status `404` · Not found · not retryable.
Nothing exists at this path, or the item asked for does not exist.

### feature-disabled
Status `404` · Feature disabled · not retryable.
The operation is switched off on this instance.

### method-not-allowed
Status `405` · Method not allowed · not retryable.
The path exists, but not for this method. The API is read-only.

### not-acceptable
Status `406` · Not acceptable · not retryable.
None of the media types in `Accept` can be served.

### payload-too-large
Status `413` · Payload too large · not retryable.
The request body is larger than the service accepts.

### uri-too-long
Status `414` · URI too long · not retryable.
The request URI is longer than the service accepts.

### unsupported-media-type
Status `415` · Unsupported media type · not retryable.
A request body was sent in a media type other than `application/json`.

### rate-limited
Status `429` · Rate limited · retryable.
Your address or key sent more requests than its rate allows. Wait
`Retry-After` seconds ([rate limits](rate-limits.md)).

### quota-exceeded
Status `429` · Quota exceeded · retryable.
The key's daily or monthly quota is used up. `Retry-After` points at the
reset.

### overloaded
Status `429` · Overloaded · retryable.
The service is shedding load by priority tier. Some instances answer `503`
instead. Wait `Retry-After` seconds.

### internal
Status `500` · Internal error · retryable.
An unexpected fault. Quote the request id.

### deadline-exhausted
Status `503` · Deadline exhausted · retryable.
The answer could not be made within the 2 second deadline.

### upstream-unavailable
Status `503` · Upstream unavailable · retryable.
Storage is unavailable.
After `breaker.failure_threshold` consecutive storage failures the service
stops asking storage for `breaker.open_for` and answers this at once;
`Retry-After` is the whole seconds left of that period. Then a few trial
requests are let through, and the first that storage answers ends it
([configuration](configuration.md)).

### not-ready
Status `503` · Not ready · retryable.
The instance is not ready to answer.
