package fixtures

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Platform writes a generated set into the platform's own tables, the ones
// the read contract's views read (the database image database.pin names
// builds both): the set's provenance, charging and parking rows. It is the
// set Generate writes, translated: the manifest, its checksum included, is
// Generate's, so a parity bundle recorded on Generate's rows names these.
//
// The translation is format-only: a source family is stored under its
// PlatformKey, a parking site under a numeric id with the generated id as its
// origin key, a charge point under its generated id as its EVSE id. What the
// views cannot carry from these tables is left out, and named in
// docs/test-data.md: a charging site's access (every site is public), a
// share-alike offer, a charge point's report from another family than its
// site's, and the tables of the other domains.
func Platform(out io.Writer, profile string, seed uint64, contractVersion string) (*Manifest, error) {
	t := &translator{rows: map[string][]map[string]string{}}
	_, m, err := generateWith(io.Discard, profile, seed, contractVersion, t.capture)
	if err != nil {
		return nil, err
	}
	w := &platformWriter{out: out}
	w.line("-- Synthetic fixtures in the platform's tables, written by viafrei-api fixturegen " + GeneratorVersion +
		". Fictional data, CC0 1.0. Profile " + profile + ", seed " + fmt.Sprint(seed) + ", rows of " + m.Checksum + ".")
	t.provenance(w)
	t.charging(w)
	t.parking(w)
	if w.err != nil {
		return nil, fmt.Errorf("fixtures: write: %w", w.err)
	}
	return m, nil
}

// PlatformKey is the platform source key a fixture source family is stored
// under. The read contract derives a source's domain from its key, so a
// family of the energy domain carries the word the energy pattern matches;
// the family no plan may use carries a key whose rows the contract never
// maps. Every other family keeps its name.
func PlatformKey(ref string) string {
	switch {
	case strings.HasPrefix(ref, "src-epsilon-"):
		return ref + "_ris_never"
	case strings.Contains(ref, "-energy"):
		return ref + "-charging"
	}
	return ref
}

// FixtureRef is the fixture family a platform key stands for: the inverse of
// PlatformKey.
func FixtureRef(key string) string {
	return strings.TrimSuffix(strings.TrimSuffix(key, "-charging"), "_ris_never")
}

// AcceptanceRegister is the register row whose sources the read contract
// says need their terms accepted; the acceptance family is stored under it.
const AcceptanceRegister = "toll_collect_sid"

// familyLicence is the licence label each family's sources carry: the read
// contract derives a licence's category and share-alike from its label.
var familyLicence = map[string]string{
	"alpha": "CC BY 4.0 (synthetic alpha)", "beta": "CC BY-SA 4.0 (synthetic beta)",
	"gamma": "CC BY 4.0 (synthetic gamma)", "delta": "Synthetic terms delta",
	"epsilon": "CC BY 4.0 (synthetic epsilon)", "zeta": "CC0 1.0 (synthetic zeta)",
	"eta": "dl-de/by-2-0 (synthetic eta)",
}

// translator holds the captured rows of the generated set, per table.
type translator struct {
	rows map[string][]map[string]string
}

func (t *translator) capture(table string, cols, vals []string) {
	r := make(map[string]string, len(cols))
	for i, c := range cols {
		r[c] = vals[i]
	}
	t.rows[table] = append(t.rows[table], r)
}

// platformWriter writes one insert per table, every row in it.
type platformWriter struct {
	out io.Writer
	err error
}

func (w *platformWriter) line(s string) {
	if w.err == nil {
		_, w.err = io.WriteString(w.out, s+"\n")
	}
}

func (w *platformWriter) insert(table string, cols []string, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	vals := make([]string, len(rows))
	for i, r := range rows {
		vals[i] = "(" + strings.Join(r, ", ") + ")"
	}
	w.line("insert into " + table + " (" + strings.Join(cols, ", ") + ") values\n  " + strings.Join(vals, ",\n  ") + ";")
}

// unquote is the text of a string literal str wrote.
func unquote(lit string) string {
	return strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(lit, "'"), "'"), "''", "'")
}

// key is the literal of the platform key of the family literal ref.
func key(ref string) string { return str(PlatformKey(unquote(ref))) }

func (t *translator) provenance(w *platformWriter) {
	publisher := map[string]string{}
	for _, p := range t.rows["publisher"] {
		publisher[unquote(p["publisher_id"])] = p["display_name"]
	}
	// One register row per family, from the first of its sources; the
	// acceptance family takes the register row the contract knows as one.
	seen := map[string]bool{}
	var register [][]string
	for _, s := range t.rows["source"] {
		family := strings.TrimPrefix(unquote(s["publisher_id"]), "pub-")
		if seen[family] {
			continue
		}
		seen[family] = true
		classes := s["use_classes"]
		if classes == "'{}'" {
			classes = "'{consumer}'" // the register holds no empty list; the family's key is never mapped
		}
		shareAlike := boolean(strings.Contains(familyLicence[family], "BY-SA"))
		if family == "delta" {
			w.line("update public.source_register set attribution_text = " + s["attribution_text"] + ", source_url = " + s["terms_url"] +
				", licence_label = " + str(familyLicence[family]) + ", changes_statement = " + s["changes_statement"] +
				", use_classes = " + classes + ", verified_on = " + s["review_signed_on"] + ", register_version = " + s["register_version"] +
				" where source_id = " + str(AcceptanceRegister) + ";")
			w.line("delete from public.source_register_notes where source_id = " + str(AcceptanceRegister) + ";")
			continue
		}
		register = append(register, []string{str("fx_" + family), s["attribution_text"], s["terms_url"], str(familyLicence[family]),
			shareAlike, "false", s["changes_statement"], classes, "false", "false", s["review_signed_on"], s["register_version"]})
	}
	w.insert("public.source_register", []string{"source_id", "attribution_text", "source_url", "licence_label", "share_alike",
		"share_alike_database", "changes_statement", "use_classes", "consumer_answers_only", "no_redistribution", "verified_on",
		"register_version"}, register)

	registerOf := func(ref string) string {
		family := t.familyOf(ref)
		if family == "delta" {
			return str(AcceptanceRegister)
		}
		return str("fx_" + family)
	}
	notes := map[string]bool{}
	var noteRows [][]string
	for _, n := range t.rows["source_note"] {
		r := registerOf(n["source_ref"])
		k := r + n["note_kind"] + n["lang"]
		if notes[k] {
			continue // one note per register row, kind and language
		}
		notes[k] = true
		noteRows = append(noteRows, []string{r, n["note_kind"], n["lang"], n["body"]})
	}
	w.insert("public.source_register_notes", []string{"source_id", "note_kind", "lang", "body"}, noteRows)

	var feeds, states [][]string
	for _, s := range t.rows["source"] {
		family := t.familyOf(s["source_ref"])
		stale, _ := strconv.Atoi(s["stale_after_s"])
		feeds = append(feeds, []string{key(s["source_ref"]), str("fixture"), publisher["pub-"+family],
			s["terms_url"], registerOf(s["source_ref"]), str(familyLicence[family]), num(max(stale/3, 1)), "true"})
	}
	w.insert("public.feed_catalog", []string{"feed_key", "provider", "display_name", "offer_url", "licence_id", "licence_label",
		"cadence_s", "enabled"}, feeds)
	for _, f := range t.rows["source_freshness"] {
		states = append(states, []string{str("feed"), key(f["source_ref"]), f["data_as_of"], f["data_as_of"], f["data_as_of"]})
	}
	w.insert("control.feed_state", []string{"ledger", "key", "last_success_at", "last_changed_at", "updated_at"}, states)
}

// familyOf is the family of the source literal ref.
func (t *translator) familyOf(ref string) string {
	for _, s := range t.rows["source"] {
		if s["source_ref"] == ref {
			return strings.TrimPrefix(unquote(s["publisher_id"]), "pub-")
		}
	}
	panic("fixtures: no source " + ref)
}

func (t *translator) charging(w *platformWriter) {
	type site struct{ feed, location string }
	sites := map[string]site{}
	var siteRows, pointRows, stateRows [][]string
	for _, s := range t.rows["charging_site"] {
		sites[s["site_id"]] = site{key(s["source_ref"]), s["location"]}
		siteRows = append(siteRows, []string{key(s["source_ref"]), s["site_id"], s["name"], s["operator_name"], s["location"],
			s["recorded_at"], s["changed_at"], s["changed_at"], s["withdrawn_at"]})
	}
	w.insert("public.charging_sites", []string{"source_feed", "site_id", "name", "operator", "geom", "first_seen_at",
		"last_seen_at", "updated_at", "closed_at"}, siteRows)
	for _, p := range t.rows["charge_point"] {
		s := sites[p["site_id"]]
		pointRows = append(pointRows, []string{s.feed, p["point_id"], p["point_id"], p["site_id"], s.location,
			"upper(" + p["current_kind"] + ")", p["power_max_kw"]})
	}
	w.insert("public.charging_points", []string{"source_feed", "point_key", "evse_id", "site_id", "geom", "current_type",
		"max_power_kw"}, pointRows)
	for _, o := range t.rows["charge_point_state_observation"] {
		stateRows = append(stateRows, []string{o["observed_at"], o["point_id"], key(o["source_ref"]), o["availability"], "'{}'"})
	}
	w.insert("public.charging_point_status", []string{"observed_at", "evse_id", "source_feed", "status", "raw"}, stateRows)
}

// parkingKind is the platform's kind of each fixture parking kind.
var parkingKind = map[string]string{
	"garage": "car_park", "lot": "car_park", "on_street": "other", "park_and_ride": "park_and_ride",
	"rest_area": "rest_area", "truck_parking": "truck",
}

func (t *translator) parking(w *platformWriter) {
	ids := map[string]string{}
	var facilities, occupancy [][]string
	for i, f := range t.rows["parking_facility"] {
		id := num(i + 1)
		ids[f["facility_id"]] = id
		facilities = append(facilities, []string{id, key(f["source_ref"]), f["facility_id"], str(parkingKind[unquote(f["parking_kind"])]),
			f["name"], f["operator_name"], f["road_ref"], f["location"], key(f["source_ref"]), f["recorded_at"], f["changed_at"],
			f["changed_at"], f["withdrawn_at"]})
	}
	w.insert("public.parking_facilities", []string{"id", "source_feed", "external_id", "kind", "name", "operator", "road", "geom",
		"attribution_id", "first_seen_at", "last_seen_at", "updated_at", "closed_at"}, facilities)
	for _, o := range t.rows["occupancy_observation"] {
		occupancy = append(occupancy, []string{o["observed_at"], ids[o["facility_id"]], o["free_spaces"], o["total_spaces"],
			o["occupancy_status"], key(o["source_ref"]), "'{}'"})
	}
	w.insert("public.parking_occupancy", []string{"observed_at", "facility_id", "spaces_free", "spaces_total", "status",
		"source_feed", "raw"}, occupancy)
	w.line("select setval(pg_get_serial_sequence('public.parking_facilities', 'id'), " + num(max(len(ids), 1)) + ");")
}
