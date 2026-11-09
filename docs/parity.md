# MCP parity

The API answers the same facts as the matching MCP capability for the same
question (CON-007, FR-GEN-002, TEST-009). The parity harness checks that on
fixture rows, offline: no live MCP server, no network.

## The pieces

| Piece | Where |
|---|---|
| Questions: what both sides are asked, and the fixture rows they are asked on | `test/parity/questions.json` |
| Bundle: the questions and the facts the capability answered | `test/parity/bundles/<platform>.json` |
| Lock file: one bundle per platform version, with its SHA-256 | `test/parity/lock.tsv` |
| Projection rules, loader, inventory and comparator | `internal/testkit/parity` |
| Harness: seeds a throwaway PostGIS, asks the real handler, compares | `internal/testkit/parity/parity_integration_test.go` |

A bundle (`format: viafrei.parity/2`) names the side that produced its facts
(`producer`: `mcp` or `api`), for an `mcp` bundle the platform commit it
answered from (`commit`, 40 hex digits; an `api` bundle names none), the fixture rows it was taken on (`profile`,
`seed` and the SHA-256 of the generated SQL), the rule set it was projected
with (`viafrei.parity.rules/2`) and its cases. A case is one
question: the operation, its MCP counterpart, the request, the key it is
asked as (use class, scopes, source families, accepted terms), a fixed
`now`, and the expected facts.

## Projection rules

Both sides state an answer as a sorted list of facts, `name = value`. The
rule set `viafrei.parity.rules/2` (`parity.Default`):

- **Facts** are the members of `data` and the members `asOf`, `attribution`,
  `freshness`, `notes`, `page` and `warnings` of `meta`. The rest of `meta`
  (`requestId`, `generatedAt`, `language`, `query`) and `links` describe the
  request, not the answer.
- **Names** are the member path. An array element is named by its `id`,
  `family` or `code` (the first present), so order and position never matter;
  an element with none of them is named by its index.
- **Identifiers** (those members and `sourceFamily`) are trimmed and
  lower-cased.
- **Positions** (`lat`, `lon`) are rounded to five decimals.
- **Numbers** otherwise are plain decimals (`22.50` is `22.5`, `1e2` is `100`);
  prices are numbers and follow this rule.
- **States** are the codes the API answers (`available`, `garage`, `partial`).
- **Instants** are UTC, RFC 3339.
- **Lists of values** (families, codes, notes) are sets: sorted and stated as
  a JSON array (`["a","b"]`), so no value can be read as a separator.
  Condition and purpose notes are compared verbatim.
- **One value per name.** Two values that project onto one name (two array
  elements with the same key) are an error, not a fact: keeping either would
  hide a difference in the other. A case naming a fact twice is refused too.
- `null`, `true`, `false`, `[]` and `{}` are facts too: an unknown value is
  not a missing one.

## What fails

- A fact missing, extra or changed: the harness prints each one by name, for
  example `changed data[cs-05-0012].location.lat: want "50.82957", got "50.8296"`.
- No bundle pinned for the platform version in `internal/buildinfo/platform-version.txt`.
- A bundle whose bytes do not match the lock file's SHA-256, checked before
  it is parsed.
- An empty bundle, an unknown member, another format, platform or rule set,
  a producer other than `mcp` or `api`, or a case naming a fact twice.
- Fixture rows that are not the ones the bundle names.
- A bundle that does not answer `test/parity/questions.json` exactly: a
  question left out, a case that is not a question, a case asked differently
  (request, key or `now`), or other fixture rows.
- An operation that declares `x-mcp-counterpart` in `api/openapi.json` and has
  no case, or a case whose `counterpart` is not the one its operation declares
  (`TestInventory`).

The harness also plants a change in one rule (positions to four decimals)
and requires it to fail on the position facts alone, so it can say no.

## Running it

```sh
go test -tags integration -run 'TestParity|TestRegressionLock|TestInventory' -v ./internal/testkit/parity/
```

With `-v` it prints the operations and cases it compared, and which side
produced the bundle it compared.

## Two bundles, two jobs

- **Parity evidence** is the bundle pinned for the platform version in
  `internal/buildinfo/platform-version.txt`, produced by the MCP side
  (`"producer": "mcp"`). `TestParity` compares it.
- **The regression lock** is `bundles/unsynced.json`, the API's own recording
  (`"producer": "api"`). `TestRegressionLock` compares it on every run. The
  API agrees with it by construction, so it is no evidence of parity.

## Producing the MCP bundle

The platform repository carries the producer,
`packages/mcp/scripts/parity-bundle.ts`. It reads `questions.json` and the
fixture SQL this repository generates, loads them into a throwaway platform
database (`scripts/test-db.sh up <name>`) in one transaction it rolls back,
asks the real MCP tool handlers each question in-process, and projects each
answer to the facts above. The projection translates format only (units,
the field names, the platform's storage names back to the fixture's
families); a value the tool states differently stays different, and a
parameter the tool has no argument for is named on stderr. It refuses fixture
SQL whose checksum is not the questions', and a platform tree with
uncommitted changes, because the bundle names the commit it answered from.

```sh
# here: the fixture SQL the questions name (testdata/fixtures/tiny/seed.sql)
make fixtures PROFILE=tiny SEED=1
# in the platform checkout, against a test_* database
TEST_DATABASE_URL=... npx tsx packages/mcp/scripts/parity-bundle.ts \
  --questions <api>/test/parity/questions.json \
  --contract <api>/db/contract/0001_contract.sql \
  --seed <api>/testdata/fixtures/tiny/seed.sql \
  --platform X.Y.Z --out <api>/test/parity/bundles/X.Y.Z.json
```

It prints the lock line on stdout; add it to `test/parity/lock.tsv`.

## Recording the regression lock

```sh
go test -tags integration -run TestRegressionLock ./internal/testkit/parity/ -parity.record
```

It rewrites `test/parity/bundles/unsynced.json` and its lock line, never a
platform version's bundle. A new question is a new entry in
`test/parity/questions.json`; record the lock and produce the MCP bundle
again.
