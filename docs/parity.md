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
with (`viafrei.parity.rules/4`) and its cases. A case is one
question: the operation, its MCP counterpart, the request, the key it is
asked as (use class, scopes, source families, accepted terms), a fixed
`now`, and the expected facts.

## Projection rules

Both sides state an answer as a sorted list of facts, `name = value`. The
rule set `viafrei.parity.rules/4` (`parity.Default`):

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
  Purpose notes are compared verbatim.
- **Condition notes** are stated by the family each belongs to:
  `meta.notes.condition[<family>]` is how many distinct condition notes of
  that family the answer carries, and `meta.notes.condition` is `[]` when it
  carries none. The text is not compared (below); which families have a
  condition and how many each has are. The API's answer states its notes as
  texts, so the harness reads which family owns each text from the fixture
  rows (`contract.source_note`; every kind but `purpose`, because the service
  answers an unknown kind as a condition). A text no family owns is an error,
  not a fact. A bundle stating a condition note's text, or a count that is
  not a positive whole number, is refused, and so is a case stating a kind
  both as `[]` and per family.
- **One value per name.** Two values that project onto one name (two array
  elements with the same key) are an error, not a fact: keeping either would
  hide a difference in the other. A case naming a fact twice is refused too.
- `null`, `true`, `false`, `[]` and `{}` are facts too: an unknown value is
  not a missing one.
- **Excluded facts** are left out of the comparison, each for the reason
  below. The API's projection drops them, and a bundle that states one is
  refused, so the MCP producer cannot emit one by accident. A pattern covers
  the fact it names and every fact under it; `[*]` is any element key. An
  exclusion may hold only in a case that asks a parameter: a parameter is
  asked when it is present and its value is not a number of 0 or less, which
  filters nothing.
- **Named differences** are facts both sides state differently by design,
  each with its reason (below). They are still compared: a fact differing in
  exactly the named way (the MCP side's value against the API's) is reported
  as that difference and not as a mismatch, any other difference in it is a
  mismatch, and a named difference that no case of an MCP bundle shows fails
  the harness. So a change on either side is seen.

### Excluded facts

| Fact | Why it is not compared |
|---|---|
| `data[*].access` | The MCP capability has no access concept: no charging source of the platform publishes who may use a site. Its questions therefore ask `access=all`, which is what the tool answers. |
| `data[*].chargePoints[*].current` | The MCP capability states no current (AC/DC) per point. |
| `meta.freshness` | API-only: the per-family freshness block of the read contract (ENV-003). |
| `meta.warnings` | API-only: the warnings follow from API keys and licence classes, which the MCP capability has not got (for example `partial` when a commercial key withholds a family). |
| `meta.page.hasMore`, in a case that asks `min_kw` | The MCP capability states no count of the sites a power filter keeps, so under `min_kw` it cannot say whether there are more. Without `min_kw` it states the count of the circle, and the fact is compared. |
| `meta.asOf` | Each side defines it differently: the API states the oldest `dataAsOf` of its families (ENV-004), the MCP capability the newest current point status, or its clock when none is current. Neither is a format of the other. |
| `meta.attribution[*].` `domain`, `licence`, `licenceUrl`, `publisher`, `shareAlike`, `shareAlikeOffer`, `termsUrl`, `text` | The fixture's families carry synthetic register texts; the platform credits its own register rows for the sources the producer stores each family as, so the texts cannot be equal on these rows. Which families are credited (`meta.attribution[*].family`) is compared. |
| The text of a condition note (`meta.notes.condition`) | The same reason, since rules/4: the fixture's condition notes are synthetic and the platform states its own register's condition. The family of each note and how many each family has are compared (`meta.notes.condition[<family>]`, above). |

### Named differences

| Fact | MCP | API | Why |
|---|---|---|---|
| `data[*].chargePoints[*].availability.state` | `occupied` | `reserved` | The MCP capability answers a reservation as occupied; the API keeps the reported state `reserved`, which says more (decided for the API, which keeps its richer state). |

## The questions

Every question states its `limit`, so neither side's default limit is
compared. Each asks only what the MCP tool can be asked:
`at`, `radius_m` (at most 25 km, the tools' maximum), `limit`, `min_kw`
and `access=all`.

`charging-status-age-limit` is asked at a `now` 38 minutes after a point's
last report: older than the status age limit (30 minutes) and younger than
its family's design threshold (an hour). Both sides must answer it `stale`,
so a change of either side's limit fails here.

`charging-reserved` is asked at the same place 7 minutes after a point
reported `reserved`: the named difference above, shown by a live report.

Both cases also reach the fixture row planted for crediting (generator
0.2.0, edge `foreign_unknown_report`): the newest report of `cp-00000-1` is
`unknown`, from another family (`src-beta-energy`) than its site's. It says
nothing, so that family contributed nothing and neither side credits it.

## Charging rules both sides follow

- **A point's status.** A report older than the shorter of the status age
  limit (`charging.status_max_age`, 30 minutes, the platform's
  `STATUS_MAX_AGE_MS`) and its family's design threshold is answered with
  the state `stale` and its `observedAt`, and is not counted in
  `summary.availableNow`. A report of `unknown`, or of a code the service
  cannot map, says nothing and is answered as no report (`availability:
  null`).
- **Credits.** An answer credits the family of each site it lists and of
  each report that says something; a report that says nothing credits no
  family.
- **`min_kw`** keeps the sites with a point of at least that power, and of
  each site lists only those points; `summary.chargePoints` counts the
  listed points.

**Where the status rule equals the MCP capability's.** The rule above is
the platform's `judgeStatus` (`analysis/charging-status.ts`) on the domain
the read contract holds: one stated instant per report, every report a
snapshot statement, and no ledger of runs. On that domain `judgeStatus`
dates a report by its own statement, judges it against
`STATUS_MAX_AGE_MS`, and judges our pull of it against the poll window of
its source. Outside it, `judgeStatus` has rules the contract cannot express,
which are therefore differences that remain:

- a change-only (delta) report older than its horizon is no report at all;
- a report that arrived before its source's publications were skipped (a jump) is
  stale, dated by its own statement;
- an unchanged point of a snapshot source is confirmed by a later complete
  snapshot that restated it, so it is dated by that publication;
- an operation or opening status that blocks charging answers the point as
  out of order, whatever its availability word;
- the liveness half is the source's own poll window (its interval times the
  freshness intervals), where the API uses the family's design threshold.

## Known differences that remain

None in the platform 2.6.8 bundle: `TestParity` passes. Its facts are the
2.5.24 bundle's, fact for fact; only `platform` and `commit` differ.

The 2.5.23 bundle differed in 9 facts. All 9 are closed:

- Seven came from the MCP side reading the SID reading against the
  database's clock instead of the question's `now` (platform issue #1262,
  fixed in 2.5.24): `termsRequired` in both parking cases, and the four
  `occupancy` facts of `pk-07-0012` in the accepted case.
- `meta.notes.condition` in parking-terms-pending: the API stated the gated
  family's condition while it withheld that family's occupancy. The API now
  follows the MCP capability's rule: a family's condition is stated only
  where the answer shows the data it conditions. A family whose terms are
  not accepted is listed and credited, but its occupancy is withheld, and so
  is its condition. Its purpose notes are kept. The rule holds in the place
  overview (`/v1/places/{id}/overview`) too, which reads the families of its
  parts again: a family its parking part withheld keeps its credit and loses
  its condition there as well. The overview has no MCP counterpart, so no
  case compares it; the use-case tests hold it.
- `meta.notes.condition` in parking-terms-accepted: both sides state one
  condition, with the fixture's synthetic text on one side and the
  platform register's text on the other. Rules/4 compares the family and the
  count, not the text.

The 2.5.23 bundle stays pinned as the record of that version. It was
projected with rules/3, which this harness no longer reads (`Load` refuses
it, and `lock_test.go` asserts that), so it can be
compared only by a checkout of its own time.

## What fails

- A fact missing, extra or changed: the harness prints each one by name, for
  example `changed data[cs-05-0012].location.lat: want "50.82957", got "50.8296"`.
- No bundle pinned for the platform version in `internal/buildinfo/platform-version.txt`.
- A bundle whose bytes do not match the lock file's SHA-256, checked before
  it is parsed.
- An empty bundle, an unknown member, another format, platform or rule set,
  a producer other than `mcp` or `api`, or a case naming a fact twice.
- A named difference that no case of an MCP bundle shows: one side changed,
  or no question reaches it any more.
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

### Why the producer and not the public server

The public MCP server answers from the platform's live rows, not from the
fixture, so its answers cannot be compared with what the API answers on the
fixture; a bundle of them would also publish live figures. The producer runs
the same handlers on the fixture rows instead. That it is the same code is
checked against the public server before a bundle is pinned:

1. `initialize` on the public endpoint: `serverInfo.version` is the
   platform version of the bundle.
2. `tools/list`: every tool's description and input-schema fingerprint
   (`npm run mcp:contract -- --fingerprints` in the platform checkout at the
   bundle's `commit`, the same hash applied to the public answer) is equal.
3. `tools/call` of each case's tool with that case's arguments: the answer is
   not an error. Only its member names are compared with the producer's
   answer, never its values. A member stated only where the data has it (a
   register caveat, a list of mapped facilities) may be on one side only.
4. The session is deleted.

## Recording the regression lock

```sh
go test -tags integration -run TestRegressionLock ./internal/testkit/parity/ -parity.record
```

It rewrites `test/parity/bundles/unsynced.json` and its lock line, never a
platform version's bundle. A new question is a new entry in
`test/parity/questions.json`; record the lock and produce the MCP bundle
again.
