# Test data

All test data is synthetic. It comes from the seeded fixture generator in
[`internal/fixtures`](../internal/fixtures), run by `cmd/fixturegen`. The
design and the rules are on the wiki page
[Test Data Management](https://github.com/mavrovde/viafrei-api/wiki/Test-Data-Management)
(TST-009, TST-014).

```sh
make fixtures PROFILE=ci SEED=7
# or
go run ./cmd/fixturegen -profile ci -seed 7 -out testdata/fixtures/ci
```

The command writes two files and prints the counts it wrote:

| File | Content |
|---|---|
| `seed.sql` | `insert` statements into the tables of the read-contract stub (`db/contract`). Load it in one transaction into a database built from that stub, for example `psql --single-transaction -f seed.sql`. |
| `manifest.json` | generator version, seed, profile, contract version, row counts per table, per Land and per source family, the licence class of each family, the planted edge cases, the licence of every input, and the SHA-256 of `seed.sql` |

The same generator version, profile and seed give the same bytes on every
machine. `testdata/fixtures/` is ignored by git: a set is rebuilt from its seed,
never committed.

`test/fixtures/checksums.tsv` records the checksum of seed 1 of every profile,
taken on one machine and architecture and checked equal on another before it
was committed. The unit tests regenerate the `tiny` and `ci` sets against it,
and CI regenerates all three on its own runner:

```sh
make fixtures-verify   # go run ./cmd/fixturegen -verify test/fixtures/checksums.tsv
```

A change to the generator's output bumps `fixtures.GeneratorVersion` and
records the lines again; a line recorded for another generator version fails.
The generator's production code may import only the packages listed in
`internal/conformance/fixture_inputs_test.go`: no network, database, process
or embedded-file package, so it reads nothing but its seed. Every generated
name parses as syllables of the generator's own lists
(`TestNamesAreBuiltFromOurSyllablesOnly`).

## Profiles

| Profile | Rows (seed 7) | Used by |
|---|---|---|
| `tiny` | about 3 000 | unit and integration tests |
| `ci` | about 44 000 | integration, contract and fuzzing tests |
| `benchmark` | about 6.5 million, seven daily partitions | benchmarks and performance tests |

## What every profile contains

- Rows in all sixteen Länder. The Länder are a 4 x 4 grid of cells over the
  bounding box of Germany, with official-format two-digit codes and generated
  names; they are not the real borders.
- Seven fictional source families, one per licence class: `alpha`
  attribution, `beta` share-alike, `gamma` restricted purpose, `delta` terms
  acceptance, `epsilon` not permitted for any plan, `zeta` zero, `eta`
  official. Each family has rows. No real publisher, licence text or
  attribution string is used.
- The planted edge cases listed in the manifest: both daylight-saving changes,
  UTC and Berlin midnight, month and year end, umlauts, ß, a hyphen and a long
  compound word, one name in two Länder, points on the outer corners of the
  bounding box and on a Land border, an area with no position at all, a road
  with no events, a road in two parts with one situation on each, a stale
  source per domain, an unmapped code value (and, on one road, an active
  situation with an unmapped value in each coded column: kind, severity,
  direction and road class), zero and full occupancy, and the largest price
  the price column holds.

The generator fails closed: an unknown profile is a usage error (exit 2), and
a set with an empty table, a Land or family without rows, a missing licence
class or a missing edge case is an error (exit 1), not an empty success.

## Licence

The generator's inputs (syllable lists, name endings, value distributions and
text templates) are our own work and are published under CC0 1.0. The
generated data is fictional; web addresses use the reserved `.example`
top-level domain, and the data holds no e-mail address and no credential.
