package store

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// referenceVersions reads, in one statement, one row (kind r) carrying the
// register fingerprint: a digest of every row the licence gate, the
// attribution and the notes of an answer are read from; one row (kind l)
// carrying the fingerprint of the road lamp's reference rows: which families
// cover each state's traffic messages, and each junction's state; and one row
// (kind u) carrying the fingerprint of the curated rule rows, every topic and
// every rule. The register and the reference rows are small tables, never an
// observation, so they are read whole: about 200 ms on prod (2026-11-25), so
// the versions read reads them only when one of the tables they are read
// from has a changed row (referenceProbe) or the last read is
// db.reference_versions_ttl old.
var referenceVersions = named("api_reference_versions", `
with `+sourceCTE(true)+`,
lic as (`+licenceRows+`)
select 'r', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from (
     select row(s.source_ref, s.domain, s.source_ref, s.licence_code, s.attribution_text, s.changes_statement, s.terms_url,
                s.share_alike_offer_url, s.acceptance_required, s.use_classes, s.freshness_target_s, s.stale_after_s,
                s.review_signed_on, s.register_version, s.display_class, s.audience, s.terms_gate, s.hold_reason,
                s.origin_restriction) t from src s) t),
  (select string_agg(t::text, ',' order by t::text) from (`+sourceNotes+`) t),
  (select string_agg(t::text, ',' order by t::text) from (
     select row(l.licence_code, l.licence_name, l.licence_url, l.licence_category, l.share_alike, l.attribution_required) t from lic l) t),
  (select string_agg(t::text, ',' order by t::text) from (`+publisherRows+`) t)))
union all
select 'l', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from (
     select row(m.state_code, m.source_ref, m.coverage_role, m.current_only, m.measured_on, 'open'::text, 'public'::text) t
     from message_coverage_sources m) t),
  `+junctionLandInputs+`))
union all
select 'u', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from (
     select row(t.topic_code, t.title_de, t.reviewed_on, t.title_en, 'viafrei_rules'::text, array['viafrei_rules'::text], 'open'::text, 'public'::text) t
     from driving_rule_topics t) t),
  (select string_agg(t::text, ',' order by t::text) from (
     select row(f.fact_key, f.topic_code, f.position, f.body_de, f.source_name, f.body_en, f.source_url, f.source_note_de,
                f.source_note_en, f.to_verify_de, f.to_verify_en, f.reviewed_on, f.headline, 'viafrei_rules'::text,
                array['viafrei_rules'::text], 'open'::text, 'public'::text) t
     from driving_rule_facts f) t)))`)

// rowVersions is a digest of the row versions (ctid and xmin: a row written
// anew has another, the columns are never read) of the table t.
func rowVersions(t string) string {
	return `(select string_agg(t.ctid::text || ':' || t.xmin::text, ',' order by t.ctid) from ` + t + ` t)`
}

// registerProbe is a digest of the row versions of every table the source
// families of the register are read from (sourceCTE: the feed catalogue, the
// registered sources and the register) and of the definitions of the views
// in between (their rewrite rows: a view replaced has a new one), so a
// register read that would read the same rows is not run again.
var registerProbe = `md5(concat_ws('|',
  ` + rowVersions("feed_catalog") + `,
  ` + rowVersions("ingestor.sources") + `,
  ` + rowVersions("source_register") + `,
  (select string_agg(c.relname || ':' || r.xmin::text, ',' order by c.relname, r.oid)
     from pg_rewrite r join pg_class c on c.oid = r.ev_class join pg_namespace n on n.oid = c.relnamespace
    where n.nspname = 'public' and c.relname = any(array['contract_sources', 'registered_sources', 'contract_source_rules', 'contract_source_domains']))))`

// referenceProbe is the probe of the rows referenceVersions reads: the
// register's (registerProbe), the row versions of the notes, the licence
// texts, the coverage of traffic messages and the curated rules, and each
// family's interval, which the register's freshness target is derived from.
// A row that changes changes the probe, so the reference fingerprints are
// read again; a row rewritten unchanged (a vacuum full, an update to the
// same values) only reads them once more. The location code list and the
// gazetteer the junctions' states are derived from are not probed, too
// large to probe on every versions read (11 000 rows): a reload of them
// reaches the lamp's fingerprint within db.reference_versions_ttl.
var referenceProbe = `md5(concat_ws('|', ` + registerProbe + `,
  ` + rowVersions("source_register_notes") + `,
  ` + rowVersions("licence_texts") + `,
  ` + rowVersions("message_coverage_sources") + `,
  ` + rowVersions("driving_rule_topics") + `,
  ` + rowVersions("driving_rule_facts") + `,
  (select string_agg(f.source_ref || '=' || coalesce(f.confirm_interval_s::text, ''), ',' order by f.source_ref) from fresh f)))`

// freshnessVersions reads, in one statement, a row per source family with its
// version (kind f, familyVersion) and with the version of its newest
// confirmation (kind c, confirmationVersion), and one row (kind p) carrying
// the probe of the reference rows (referenceProbe). It reads the feed status
// and the row versions of small tables only: the versions read the cache
// waits for once a second (cache.versions_ttl).
var freshnessVersions = named("api_freshness_versions", `
with fresh as (`+freshnessRows+`)
select 'f', f.source_ref::text, `+familyVersion+` from fresh f
union all
select 'c', f.source_ref::text, `+confirmationVersion+` from fresh f
union all
select 'p', '', `+referenceProbe)

// junctionLandInputs fingerprints what each junction's state is derived
// from, not the derived states: contract_junction_land gives a junction the
// Land of the nearest municipality of the BKG gazetteer within 20 km, so the
// junctions (their key and position) and those municipalities (their id,
// Land and position) decide every junction's state, and the fingerprint
// moves whenever any of them does (a state that stays put may move it too:
// a cached lamp then ends early, never late). Deriving every motorway
// junction's state on each read was a nearest-neighbour search per junction,
// about 3 000 of them: 230 ms of a versions read the cache waits for once a
// second (cache.versions_ttl), before every answer.
const junctionLandInputs = `(select md5(string_agg(t.cid || ':' || t.tabcd || ':' || t.lcd || '@' || md5(st_asbinary(t.geom)), ',' order by t.cid, t.tabcd, t.lcd))
     from tmc_locations t
     where t.cid = 58 and t.tabcd = 1 and t.tcd = 1 and t.road_number ~ '^A[1-9][0-9]{0,2}$' and t.geom is not null and t.name is not null),
  (select md5(string_agg(g.id::text || '=' || left(g.admin_code, 2) || '@' || md5(st_asbinary(g.geom)), ',' order by g.id))
     from gazetteer_places g
     where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$' and g.geom is not null)`

// statusOf are the families whose freshness is the status row of the
// extract they are loaded from, as (family, status row): the OpenStreetMap
// places of interest (the car parks around a position among them) are the
// POI extract's.
const statusOf = `(values ('osm', 'osm_pois'))`

// freshnessRows are each family's feed status (control.data_status, the
// platform's feed status view) whose source rule does not exclude it: the
// instant its data last changed and its version, its last confirmation,
// the interval and whether its updates are paused.
// A family's rule is the first matching one by priority (the priorities are
// distinct): it excludes the family when an excluding rule matches and no
// rule before it does, so each key is matched against the excluding rules
// first and against the others only then (one rule of six excludes:
// 304 keys took 10.9 ms matched against all six, 3.9 ms so, on prod
// 2026-11-25).
// A family the platform keeps no status row of its own for reads the row
// of the extract it is loaded from (statusOf).
const freshnessRows = `select d.key source_ref, d.last_changed_at data_as_of,
       coalesce(extract(epoch from d.last_changed_at)::bigint::text, '0') freshness_version,
       d.held_since confirmed_at, d.cadence_s confirm_interval_s, d.paused updates_paused
  from (select x.key, x.last_changed_at, x.held_since, x.cadence_s, x.paused from control.data_status x
        union all
        select a.family, x.last_changed_at, x.held_since, x.cadence_s, x.paused
          from ` + statusOf + ` a(family, status)
          join control.data_status x on x.key = a.status
         where not exists (select from control.data_status y where y.key = a.family)) d
 where not exists (select from contract_source_rules k where k.excluded and d.key ~ k.pattern
                      and not exists (select from contract_source_rules j where j.priority < k.priority and d.key ~ j.pattern))`

// sourceNotes are the register notes of every family its rule does not
// exclude, as rows (family, kind, language, text).
const sourceNotes = `select row(s.source_ref, n.note_kind, n.lang, n.body) t
  from contract_sources s
  join source_register_notes n on n.source_id = s.register_id
 where not coalesce((select k.excluded from contract_source_rules k where s.source_ref ~ k.pattern order by k.priority limit 1), false)`

// publisherRows are the publisher of every family its rule does not
// exclude, as rows (family, name, website).
const publisherRows = `select row(s.source_ref, s.publisher_name, coalesce(g.source_url, s.terms_url)) t
  from contract_sources s
  left join source_register g on g.source_id = s.register_id
 where s.publisher_name is not null
   and not coalesce((select k.excluded from contract_source_rules k where s.source_ref ~ k.pattern order by k.priority limit 1), false)`

// licenceRows are every licence a published family names or a licence text
// holds, with its link, category, share-alike and attribution, over src.
var licenceRows = `select l.code licence_code, l.code licence_name, ` + licenceURL("l.code") + ` licence_url,
       case when l.code ~* '(CC0|Creative Commons Zero|zero-2-0|Datenlizenz Deutschland – Zero)' then 'public_domain'
            when l.code ~* '(BY-SA|ODbL|Open Database License)' then 'share_alike'
            when l.code ~* '(CC BY|Namensnennung|by-2-0|GeoNutzV)' then 'attribution'
            else 'terms' end licence_category,
       l.code ~* '(BY-SA|ODbL|Open Database License)' share_alike,
       not l.code ~* '(CC0|Creative Commons Zero|zero-2-0|Datenlizenz Deutschland – Zero)' attribution_required
  from (select s.licence_code code from src s where s.licence_code is not null union select t.licence_label from licence_texts t) l`

// confirmationVersion is the version of a family's newest confirmation, over
// the feed status as f (freshnessRows): the instant, the interval and whether its
// updates are paused. The road lamp judges a family's sight by them, so a
// confirmation that recovers a family, or a changed interval, ends a cached
// lamp; a confirmation lapsing with the clock alone is an instant the answer
// notes instead. Kept apart from familyVersion, so an answer that does not
// judge confirmations is not ended by every one.
const confirmationVersion = `coalesce(extract(epoch from f.confirmed_at)::text, '') || '@' || coalesce(f.confirm_interval_s::text, '') || '@' || f.updates_paused::text`

// familyVersion is the version of a family's data that the HTTP cache
// compares and an ETag is built from, over the status row as f (freshnessRows):
// the freshness version AND the instant of the data's last change
// (data_as_of). An answer renders Last-Modified from data_as_of, so a moved
// data_as_of must end a cached answer even when the freshness version
// stayed the same (a source clock that was wrong and is corrected, say).
// It does not move with a confirmation: an answer dates its freshness entry
// at the later of the change and the newest confirmation
// (provenance.Family.KnownCurrent), and a cached answer that states a family
// stale is also ended by that family's next confirmation (its
// confirmationVersion), while a fresh one ends when it turns stale.
// Empty when the family has published nothing; Provenance reads the same
// expression, so an answer is tagged with the version this read compares.
const familyVersion = `coalesce(f.freshness_version || '@' || coalesce(extract(epoch from f.data_as_of)::text, ''), '')`

// references is the last read of the reference fingerprints
// (referenceVersions): the probe it was read under and when it was read.
type references struct {
	ttl time.Duration

	mu       sync.Mutex
	read     bool
	probe    string
	at       time.Time
	register string
	lamp     string
	rules    string
}

// ReferenceVersions reports how long the reference fingerprints are kept
// while their probe stays (db.reference_versions_ttl).
func (s *Store) ReferenceVersions() time.Duration { return s.refs.ttl }

// FreshnessVersions reads the freshness version and the confirmation version
// of every source family, the register fingerprint and the lamp's and the
// rules' reference fingerprints, in one snapshot. The family rows are read
// every time, from the feed status, a small table; the fingerprints, an
// aggregate over every register row, are read again in the same snapshot
// only when the probe of the rows they are read from moved or the last read
// is db.reference_versions_ttl old, and are the last read's otherwise
// (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (provenance.Versions, error) {
		// Built inside the read and handed back only to a caller still
		// waiting: nothing the caller holds is written past its deadline.
		out := provenance.Versions{Families: map[string]string{}, Confirmations: map[string]string{}}
		var kind, family, version, probe string
		// A failed query surfaces through ForEachRow.
		rows, _ := run(ctx, tx, freshnessVersions)
		_, err := pgx.ForEachRow(rows, []any{&kind, &family, &version}, func() error {
			switch kind {
			case "p":
				probe = version
			case "f":
				out.Families[family] = version
			default:
				out.Confirmations[family] = version
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		s.cells.noteVersions(maps.Clone(out.Families))
		at := s.nowFn()
		r := &s.refs
		r.mu.Lock()
		kept := r.read && r.probe == probe && at.Sub(r.at) < r.ttl
		out.Register, out.LampReference, out.RulesReference = r.register, r.lamp, r.rules
		r.mu.Unlock()
		if kept {
			return out, nil
		}
		rows, _ = run(ctx, tx, referenceVersions)
		_, err = pgx.ForEachRow(rows, []any{&kind, &family, &version}, func() error {
			switch kind {
			case "r":
				out.Register = version
			case "l":
				out.LampReference = version
			default:
				out.RulesReference = version
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		r.mu.Lock()
		r.read, r.probe, r.at, r.register, r.lamp, r.rules = true, probe, at, out.Register, out.LampReference, out.RulesReference
		r.mu.Unlock()
		return out, nil
	})
}
