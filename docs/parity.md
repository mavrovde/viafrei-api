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
with (`viafrei.parity.rules/5`) and its cases. A case is one
question: the operation, its MCP counterpart, the request, the key it is
asked as (use class, scopes, source families, accepted terms), a fixed
`now`, and the expected facts.

## Projection rules

Both sides state an answer as a sorted list of facts, `name = value`. The
rule set `viafrei.parity.rules/5` (`parity.Default`):

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
- **Purpose notes** follow the condition notes' rule below since rules/5:
  `meta.notes.purpose[<family>]` is how many distinct purpose notes of that
  family the answer carries, `meta.notes.purpose` is `[]` when it carries
  none, and the text is not compared (the fixture's purpose notes are
  synthetic, the platform states its own register's). A rules/4 bundle,
  which compared purpose texts verbatim, is read under rules/5 only when it
  states no purpose note: the two rule sets state such an answer alike.
- **A note text two families own** (a register's shared wording) is the note
  of the family the answer credits (`meta.attribution[*].family`): an
  answer's notes are its credited families'.
- **Condition notes** are stated by the family each belongs to:
  `meta.notes.condition[<family>]` is how many distinct condition notes of
  that family the answer carries, and `meta.notes.condition` is `[]` when it
  carries none. The text is not compared (below); which families have a
  condition and how many each has are. The API's answer states its notes as
  texts, so the harness reads which family owns each text from the fixture
  rows (`contract.source_note`; every kind but `purpose`, because the service
  answers an unknown kind as a condition). A text no family owns is an error,
  not a fact, except the notes the platform states on its own behalf (the
  §23 StVO notice on speed cameras): those are counted under `unowned`,
  whatever the answer credits. A bundle stating a condition note's text, or a count that is
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
- **An answer whose every data fact is excluded** states `data = {}`: it
  answered, and none of what it answered is compared. An answer the other
  side refuses states `error = true` instead.
- **Site-route questions.** An operation the site reads and no MCP tool
  answers (`listMotorways`, `getMeta`, `getPlaceOverview`) is
  compared with what the site shows: the producer asks the digest routes the
  site reads on the same fixture rows and projects their fragments. The API
  states what the site shows; a fact the API answers beyond it is excluded for
  that operation, with its reason below.
- **No named differences.** Every fact that is not excluded is compared
  as it is: a value either side states otherwise is a mismatch. There is no
  list of facts both sides may state differently.

### Excluded facts

| Fact | Why it is not compared |
|---|---|
| `data[*].access` | The MCP capability has no access concept: no charging source of the platform publishes who may use a site. Its questions therefore ask `access=all`, which is what the tool answers. |
| `data.access`, in `getChargingSite` only | The single site's `data[*].access`, for the same reason. |
| `data[*].chargePoints[*].current`, and `data.chargePoints[*].current` in `getChargingSite` | The MCP capability states no current (AC/DC) per point. |
| `meta.freshness` | API-only: the per-family freshness block of the read contract (ENV-003). |
| `meta.warnings` | API-only: the warnings follow from API keys and licence classes, which the MCP capability has not got (for example `partial` when a commercial key withholds a family). |
| `meta.page.hasMore`, in a case that asks `min_kw` | The MCP capability states no count of the sites a power filter keeps, so under `min_kw` it cannot say whether there are more. Without `min_kw` it states the count of the circle, and the fact is compared. |
| `meta.asOf` | Each side defines it differently: the API states the oldest `dataAsOf` of its families (ENV-004), the MCP capability the newest current point status, or its clock when none is current. Neither is a format of the other. |
| `data.station.location`, in `listStationDepartures` only | The MCP departure board states the stop's name and code, not its position. The site reads the station's position from the digest route `/internal/station/:slug`, so the API answers it; it is not compared. |
| `data[*].location`, `data[*].districtCode`, `data[*].modes`, in `listStations` only | The MCP stop search states a stop's name, code and distance, not its position, district or modes. The site reads all three from the digest route `/internal/station/:slug` (the page's map and Land line from the stop's position and AGS, the live board's mode filter), so the API answers them; they are not compared. A position in any other operation, a charging site's or a car park's, is compared. |
| `data[*].eva`, `data[*].evaNumbers`, in `listStations` only | The MCP stop search states a stop's name and codes, not its EVA numbers. The site reads them from the digest route `/internal/station/:slug` (the station page's DB board and its parts), so the API answers them; they are not compared. |
| `data.zones`, in `getDrivingRules` only | API-only. The MCP driving rules state the verdict at a position (`data.zoneVerdict`, compared) and, for a city, the curated zone list's entry; neither they nor any digest route the site reads list the published zone rows (key, role, publisher, distance) the API lists for a client to draw. |
| `data.lamp`, `data.truncated`, in `getWeatherRisk` only | API-only: the /v1 contract states the lamp over every warning in force at the position and whether the list was cut at `weather.max_warnings`, and a change inside /v1 may not remove them (VER-001). The platform's weather warnings check states neither, and no digest route the site reads serves a lamp for a position. The warnings and the credited families are compared. |
| `data.lamp`, `data.lampReason`, in `listStateRoadConditions` only | The MCP motorway status of a Land lists the Land's items and counts, not a lamp. The site reads the Land's lamp (the motorway pages' one rule over the Land's Autobahn items and DWD levels) from the digest route `/internal/state/:slug`, so the API answers it; it is not compared. |
| `data.lamp`, `data.lampReason`, `data.corridor.states`, in `getMotorwayConditions` asked with `horizon_days` or `from` | The MCP roadworks-ahead answer lists the planned sites of the window, not a lamp or the Länder the road crosses. The site reads the road's lamp and Länder beside its planned works from the digest route `/internal/motorway/:road`, so the API answers them; they are not compared. |
| `data[*].kilometres`, `data[*].sourceFamily`, in `listMotorways` only | API-only. The site's motorway list, index and pages (digest routes `/internal/motorway/list`, `/internal/motorway/index`, `/internal/motorway/:road`) show no length and no register family of a motorway. What they show (the road, its junctions with number, kind, name, point and Land, the Länder it crosses, its count of reports, its lamp, and the credits of the index) is compared. |
| `data.apiVersion`, `data.buildSha`, `data.contractVersion`, `data.languages`, `data.openapi`, `data.platformVersion`, in `getMeta` only | API-only: the versions and the document of the running service. The site reads only whether the service is saturated (digest route `/internal/pressure`); the API states that by answering at all. The answer is compared as `data = {}` (below). |
| `data.bays[*].distanceM`, in `listParkingBays` only | The parking tool states a bay's distance in kilometres to two decimals, the API in metres, so the two agree only to the tool's 10 m. The bay's point, which both measure from, is compared. |
| `data.departures[*].line`, `data.departures[*].number`, in `getTrainBoard` only | The train tool states one label per departure, what a person reads on the board (`ICE 571`, `RE 1`); the API states the board's line and train number apart. A label of a category and a number cannot be split back into the two. Category, destination, times, delay, platforms and cancellation are compared. |
| `data.fresh`, `data.fetchedAt`, `data.ageSeconds`, in `getTrainBoard` only | API-only: the age of the recorded board the API serves. The train tool asks the timetable provider at the time of the question; the producer gives it a departures port fed from the fixture's recorded boards (`contract.train_board`), so both sides answer from the same rows and no provider is called. |
| `data.place.location`, `data.charging.sites[*].access`, `data.charging.sites[*].chargePoints`, `data.parking.sites[*].kind`, `data.parking.sites[*].operator`, `data.parking.sites[*].occupancy.total`, `.state`, `.observedAt`, in `getPlaceOverview` only | API-only. The site's form of the overview is the place page of the Gemeinde (digest route `/internal/place/cards`): it shows the place's name, and per charging site its name, operator, map point, highest power and number of charge points, per car park its name, map point and free spaces. Those are compared; the place's position, a charging site's access and charge points, and a car park's kind, operator, capacity, state and reading time are not shown. A site's id is the read contract's row at the point the row links its map to. |
| `data.charging.sites[*].distanceM`, `data.parking.sites[*].distanceM`, in `getPlaceOverview` only | The cards state a site's distance in kilometres to one decimal with a direction, the API in metres. The site's point, which both measure from, is compared. |
| `data[<family>]` and `meta.attribution[<family>]` of the reference families `bast_lcl`, `bkg_gvisys`, `delfi_static`, `geonames`, `osm`, `osm_speed_cameras`, `viafrei_rules`, and `data[*].domain`, in `listSources` only | API-only, lead ruling 2026-11-17: the coverage resource lists the deployment's live sources, not its reference data (places, the road register, timetables, rules), and states no domain. The live-source families it lists are compared: which are listed, `staleAfterS`, `shareAlike`. |
| `data[*].status`, `data[*].dataAsOf`, in `listSources` only | Live-varying. The source register's MCP form is the coverage resource (`viafrei://coverage`), generated from the source catalogue: it states a source's freshness from the platform's run ledger, the API a family's status and age from its newest `dataAsOf`. The fixture rows hold no runs, so the two cannot agree on these rows. Which families are listed, their `domain`, `staleAfterS` and `shareAlike` are compared. |
| `data[*].attribution`, `data[*].licence`, `data[*].licenceUrl`, `data[*].publisher`, `data[*].termsUrl`, in `listSources` only | Prose: the register texts of a family. The fixture's families carry synthetic texts and the platform states its own register's (the reason of `meta.attribution[*]` below). |
| `meta.notes`, in `listSources` only | Register texts, lead ruling 2026-11-17: the condition and purpose notes a source list carries are the register's prose about its families, left out of the sources comparison like the texts above; the coverage resource states none. |
| `data.watchId`, `data.uri`, in `getWatch` only | Each side mints the id of a watch: the platform a number of the session that opened it, the API a token of the kind, the key and the condition. The watch the id names (kind, key, condition, window) is compared. |
| `meta.attribution[*].` `domain`, `licence`, `licenceUrl`, `publisher`, `shareAlike`, `shareAlikeOffer`, `termsUrl`, `text` | The fixture's families carry synthetic register texts; the platform credits its own register rows for the sources the producer stores each family as, so the texts cannot be equal on these rows. Which families are credited (`meta.attribution[*].family`) is compared. |
| The text of a condition note (`meta.notes.condition`) and, since rules/5, of a purpose note (`meta.notes.purpose`) | The same reason, since rules/4 for conditions: the fixture's notes are synthetic and the platform states its own register's. The family of each note and how many each family has are compared (`meta.notes.<kind>[<family>]`, above). |

A question the API refuses as unknown (`404`, a problem document) is
compared as one fact, `error = true`, as the MCP side states a tool's
`not_found`; the wording of either refusal is not compared. Any other
status fails the run.

## The questions

Every question states its `limit`, so neither side's default limit is
compared. Each asks only what the MCP tool can be asked:
`at`, `radius_m` (at most 25 km, the tools' maximum), `limit`, `min_kw`
and `access=all`.

`charging-status-age-limit` is asked at a `now` 38 minutes after a point's
last report: older than the status age limit (30 minutes) and younger than
its family's design threshold (an hour). Both sides must answer it `stale`,
so a change of either side's limit fails here.

A question may ask a **refresh** before its read (`refresh`: the
`source` and `subject` of `createRefresh`). The harness asks
`POST /v1/refreshes` as the question's key (an own key: only ViaFrei's own
keys may ask one), the fake live refresher (`internal/testkit/fakelive`)
settles it by the repository's script (`test/live/fakelive.json`), and the
read is asked once it has settled `refreshed`; any other outcome fails the
run. The read is compared, not the refresh's own answer: the MCP tool
states no refresh of its own. The MCP side settles the same script's ticks
on its rows at the question's clock, through the tool's own port (the fuel
tools' price refresh, the train board's provider). Both sides undo the
refresh after the read, so every pass over the questions reads the same
rows; a refresh question is asked at a clock no fixture row is dated.
`fuel-refreshed` and `train-board-refreshed` are asked so.

`sources-all` is asked with ViaFrei's own consumer key: the MCP coverage
resource lists the deployment's sources for the site's and an assistant's
readers, which is that key; a commercial or public key would withhold the
consumer-only and own-only families the resource lists.

**API-only questions.** A question stating `apiOnly` (its reason) has no
MCP form. The API's recording keeps it as a regression golden; a platform
bundle neither answers it nor may state it. `sources-places` (the domain
filter), `places-coverage` and `national-summary` are api-only (lead ruling
2026-11-17): site parity of the last two comes with the web switch to the
API. A question's `ruling` keeps the lead's ruling on how it is compared.

`charging-reserved` is asked at the same place 7 minutes after a point
reported `reserved`. Both sides must answer it `occupied`: a reserved point
is not free to charge at, and no answer states `reserved` (#18).

Both cases also reach the fixture row planted for crediting (generator
0.2.0, edge `foreign_unknown_report`): the newest report of `cp-00000-1` is
`unknown`, from another family (`src-beta-energy`) than its site's. It says
nothing, so that family contributed nothing and neither side credits it.

## Charging rules both sides follow

- **A reserved point** is answered `occupied`. `reserved` stays in the open
  enumeration of the contract, because removing a value a response may carry
  is a breaking change (VER-002), but no answer states it.
- **A point's status.** A report older than the shorter of the status age
  limit (`charging.status_max_age`; the MCP capability's status age limit,
  30 minutes) and its family's design threshold is answered with
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

- **An empty answer credits no family**, as the platform's searches credit
  the registers whose rows they state.

## Other rules both sides follow

- **Parking.** A site whose terms are not accepted is not listed; the
  answer offers its terms. A Toll Collect site, once accepted, is listed
  beside the other sites with no distance (`distanceM: null`), and an
  answer with no other site lists none. A search along a motorway lists no
  distance and credits the location-code points (`bast_lcl`) its corridor is
  drawn from. An empty answer credits the families with a site of the kind
  asked in the circle, else of any kind, before the free-space and amenity
  filters.
- **Speed cameras.** An answer listing nothing credits no family, and every
  answer carries the §23 StVO notice as a condition.
- **Sharing.** No station beyond the radius is named.
- **Hydrogen.** A station has no id: the platform names it by name and
  position. The vehicle filter matches the AFIR solution's ending (`Car`,
  `Truck`).
- **Cheapest fuel** (`sort=price`) lists only the asked grade's price, and
  each station's reported open state.
- **Addresses** carry no identifier and no reading of the typed text: the
  platform's address search states neither, so an address is named by its
  position in the answer.
- **Places of interest** carry no identifier and no `atLeast` flag; an
  answer that lists none credits no family. Equal matches rank by trigram
  similarity to the asked name, then by name, as the platform's search
  ranks them.
- **What is around** (`listNearby`): a vehicle category or the junctions
  that list nothing credit no family; the needs credit the families they
  consulted. A need's place carries no identifier and its distance is stated
  to 0.1 km, as the platform's nearby search states them.
  A filling station states its E5 price and no opening state, and a station
  is a railway station of the DB station register (an EVA number), as the
  platform's nearby search lists them.
- **Transit disruption.** An alert states its headline, lines, validity,
  placement and link, as the platform's check does: no text beyond the
  headline, no cause or effect, no change time. The answer credits each
  alert it lists and, for an area, the realtime family it read the
  punctuality of, a region with no report included; an answer that lists no
  alert and reads no punctuality credits no family.
- **Road conditions near a position** list what the platform's road status
  lists there: closures, roadworks and lane restrictions, and jams, each
  distance to 0.1 km. A hazard or an accident is not listed, and the answer
  states no lamp and reads no weather warning: the road status states none
  around a position, and no digest route the site reads serves one there.
- **Weather warnings** at a position are listed as the platform's weather
  warnings check lists them. Its lamp and truncation flag are API-only (the
  /v1 contract keeps them; excluded above). A family the platform does not
  read is not credited.
- **Departure boards** state the station by the read contract's id, its
  name and its national stop code, and the window asked. A day the
  timetable holds no trip on is inside the window and answers no departure.
- **Motorway operator events.** The fixture family the producer stores as the
  Autobahn GmbH's events (`roads_alpha`) carries only rows the platform can
  hold for that source (fixture generator 0.3.2): a closure, roadworks, or a
  hazard, and no end, each last seen by the family's newest sweep (its
  `data_as_of`). The platform's read contract (`contract.traffic_event`)
  states an Autobahn warning as kind `hazard`, severity `info`, an item as
  current while its sweep lists it, and an item's change time (`updatedAt`)
  as that sweep's instant, which is all the platform's motorway status
  states of it; the projection of the MCP answer uses that translation,
  never its own.
- **A watch** reads the person's words as the platform does when the
  condition names no events: a jam only watches jams, a jam beside a
  closure or a restriction watches both, and a jam is watched on a motorway
  only. It states no lamp (the platform's watch states none and no digest
  route serves one) and credits the families of the situations it reports,
  none when it reports none, as the platform's watch names the source of
  the data behind each report.
- **Driving rules** are stated in the answer's language, each with its key,
  its source, its own review date and what of it is not confirmed, and the
  topic's review date is its oldest rule's, as the platform's driving rules
  state them. The platform's disclaimer opens with that date, which the
  API states as reviewedOn; the rest is compared.
- **A weather warning is on a motorway** when its own area holds one of the
  road's junction points, as the platform's motorway page and motorway
  status place a DWD warning on the road's location-code points; a district
  the road crosses does not place it.
- **Road conditions credit the weather.** The lamp reads the DWD warnings
  of every weather family the key may read, with or without the weather
  scope (the warnings are public), and the answer credits those families
  whether or not a warning is in force, as the platform's motorway status
  credits the DWD.
- **Lorry rules** carry no identifier, so a row is named by its place in
  the answer (by distance, none last). A publisher's file date is stated
  for a priority route and the long-lorry network only, and the long-lorry
  network states no direction: the platform's road status states neither.
  A priority route is the nearest street of its stretch.
- **Station facilities** name the station only and state no report
  instant per unit, as the platform's facilities check states them.
- **A window ahead reads the planned works alone.** A road-conditions
  answer over a window ahead (`horizon_days`, or `from` and `to`) reads only
  the families of planned works (`roads.planned_families`, BASt's planning
  data and its twins) and no weather warning, as the platform's
  roadworks-ahead answer reads the planning data alone; a site changed at
  its source's last check.
- **Placement credits BKG.** A Land's road conditions credit the family
  that placed its situations in the Land (the read contract's
  `placement_source_ref`, BKG's municipalities), and a station name that
  more than one station answers credits the family that places each in its
  district and Land, as the platform credits BKG on its Land form and on
  the town and Land each name candidate is labelled with.
- **A place search lists stops.** A name answers the timetable's stops too
  (kind `station_area`, the stop families the key may read), and a
  distance is measured on the sphere, as the platform's place search lists
  the stop register's stops and measures great-circle metres. A place
  carries no id on the MCP side: the producer reads it by name and point.

The MCP side has further rules, specific to its sources, that the read
contract cannot express; they are not compared here.

No question asks the departures of one train category (`categories`,
the platform's `get_train_departures`). The platform answers it from the DB
Timetables interface, a live provider call the producer never makes; without
that call the tool answers that train departures are not enabled, or, when
the call fails, the timetable as a fallback. Neither is the answer it gives,
so the case is not asked. The API's category filter is held by its own
tests; the departures cases compare the board it filters.

## Known differences that remain

None in the platform 2.6.18 bundle: `TestParity` passes with zero
differences, `charging-reserved` included. Its facts are the 2.6.13
bundle's, case for case and fact for fact; only `platform` and `commit`
differ. Until #18 one fact differed by
design: the API stated `reserved` where the MCP side answers `occupied`;
the API now answers `occupied` too. The bundle's facts
are the 2.6.12 and 2.6.8 bundles', and so the 2.5.24 bundle's, fact for
fact; only `platform` and `commit` differ.

The 2.5.23 bundle differed in 9 facts. All 9 are closed:

- Seven came from the MCP side reading the SID reading against the
  database's clock instead of the question's `now` (fixed in platform
  2.5.24): `termsRequired` in both parking cases, and the four
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

The 2.5.23 and 2.5.24 bundles were answered from producer commits made after
those versions' tags (each names its commit in `commit`). They are records
only, not parity evidence; the evidence is the bundle of the pinned platform
version (2.6.18), answered at that tag's commit, as the 2.6.8, 2.6.12 and
2.6.13 bundles were at their own.

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

The platform's parity producer makes the MCP side's bundle.

- **Inputs:** `test/parity/questions.json`, the read-contract schema as
  the service builds it (`contract.SchemaSQL`: `db/contract/0001_contract.sql`
  followed by `db/contract/0003_physical.sql`, in one file, because the
  relations alone have partitioned observation tables with no partition a
  fixture row can be written into), the fixture SQL the questions name
  (`make fixtures PROFILE=tiny SEED=1` writes it to
  `testdata/fixtures/tiny/seed.sql`) and the platform version.
- **What it does:** it loads the fixture rows into a throwaway database, asks
  the MCP capability each question with the question's `now`, and projects
  each answer to the facts above. The projection translates format only
  (units, field names, the fixture's families); a value the capability
  states differently stays different.
- **Refusals:** fixture SQL whose checksum is not the questions', and a
  platform tree with uncommitted changes, because the bundle names the
  commit it answered from.
- **Outputs:** `test/parity/bundles/X.Y.Z.json`, and its line for
  `test/parity/lock.tsv`.

### Why the producer and not the public server

The public MCP server answers from the platform's live rows, not from the
fixture, so its answers cannot be compared with what the API answers on the
fixture; a bundle of them would also publish live figures. The producer runs
the same handlers on the fixture rows instead. That it is the same code is
checked against the public server before a bundle is pinned:

1. `initialize` on the public endpoint: `serverInfo.version` is the
   platform version of the bundle.
2. `tools/list`: every tool's description and input schema, hashed the same
   way on both sides, equal the platform's at the bundle's `commit`.
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
