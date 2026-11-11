# Getting started

From nothing to a first answer. The API is **not deployed yet**: there is no
public instance and no key is issued today. Everything below runs on your own
machine against synthetic data, and the steps for a hosted key are the plan,
not a service you can use now.

## 1. Run an instance

You need a container runtime and the repository.

```sh
cp .env.example .env    # replace both placeholders
make up                 # builds the API and a database with synthetic data
curl -s localhost:8080/healthz
```

The local stack answers the open operations (`/healthz`, `/readyz`,
`/v1/meta`, `/v1/openapi.json`). It runs without an access store, so every
keyed operation answers `404 feature-disabled` until you configure one
([container](container.md), [API keys](access-keys.md)).

## 2. Get a key

On a hosted instance a key will be issued to you by the maintainers, for a
plan that names the data families you may read, your use class and your rate
and quotas. On your own instance you issue one yourself with the operator's
key tool, once an access store is configured:

```sh
viafrei-api keys plan  -name trial -use-class consumer -tier standard -scopes energy,parking,places \
                       -sources src-alpha-energy,src-alpha-parking,src-alpha-places -rate 5 -burst 10 -actor me
viafrei-api keys issue -plan trial -label "My test app" -contact me@example.test -actor me
```

The full key is printed **once**. Keep it in a secret store or an environment
variable, never in source code:

```sh
export VIAFREI_API_KEY='…'   # the key the tool printed
```

> **A key used from a browser is visible to its user.** Anyone who opens the
> page can read it from the network panel and use your quota. Call the API
> from your server and pass only the answer on to the browser.

## 3. Make the first call

```sh
curl -sS "http://localhost:8080/v1/key" -H "Authorization: Bearer $VIAFREI_API_KEY"
```

The answer describes your key: its plan, its expiry and the source families
the licence gate lets it receive, each with the attribution you must show.
Then ask for data:

```sh
curl -sS "http://localhost:8080/v1/charging-options?at=50.82,6.0&radius_m=20000&limit=2" \
  -H "Authorization: Bearer $VIAFREI_API_KEY"
```

Every operation, with a request and the answer the service gives to it, is
listed on the [endpoints](endpoints.html) page and in the
[API reference](reference.html).

## 4. Read an answer

Every data answer has the same three members:

| Member | What it holds |
|---|---|
| `data` | the answer itself |
| `meta` | the request id, when the answer was made, how fresh each source family's data is, the **attribution** of every family that contributed, licence **notes**, and **warnings** |
| `links` | the answer's own URL |

Before you display the data, read [attribution and licence rules](attribution.md):
showing `meta.attribution` and `meta.notes` with the data is a condition of
use, not a courtesy.

Clients must ignore members they do not know: new members can appear in v1
without notice ([versioning](versioning.md)).

## 5. When something goes wrong

Every error is an RFC 9457 problem with a stable `type`; the
[error codes](errors.md) page lists them all. A `429` or `503` carries
`Retry-After`: wait that long, then retry. Every request has a hard deadline
of 2 seconds, so the service never keeps you waiting longer
([rate limits](rate-limits.md)).
