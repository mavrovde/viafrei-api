package roads

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// SpeedCamera is a published speed-check location: a town's official
// list (a fixed camera, a section control, an announced mobile check, a
// measuring site) or a fixed camera OpenStreetMap maps.
type SpeedCamera struct {
	ID            string
	Kind          string
	Enforces      *string
	Street        *string
	LocationText  *string
	Municipality  *string
	Direction     *string
	MaxSpeedKmh   *int
	MaxSpeedHGVKm *int
	ValidFrom     *time.Time
	ValidTo       *time.Time
	DataAsOf      *time.Time
	// StaleYear is the year of the publication when it is over twelve
	// months old on the day asked; nil otherwise.
	StaleYear *int
	Location  *geo.Point
	Family    string
}

// SpeedQuery asks for the published locations within RadiusM of At on the
// Europe/Berlin day Today, at most Limit of each block.
type SpeedQuery struct {
	At      geo.Point
	RadiusM int
	Today   time.Time
	Limit   int
}

// SpeedCameras are the two blocks of the answer, each with the number of
// locations before its limit.
type SpeedCameras struct {
	Official      []SpeedCamera
	OfficialTotal int
	OSM           []SpeedCamera
	OSMTotal      int
}

// The notice every speed-camera answer carries as a condition, as the
// platform's speed-camera search carries it (§ 23 Abs. 1c StVO): its seven
// elements, one string per language. German is the master.
const (
	speedNoticeDE = "Hinweis zur Nutzung (§ 23 Abs. 1c StVO): Wer ein Fahrzeug führt, darf während der Fahrt kein technisches Gerät und keine App-Funktion betreiben oder nutzen, die Verkehrsüberwachungsmaßnahmen anzeigt oder stört. " +
		"Das gilt auch für das Smartphone eines Beifahrers, wenn der Fahrer davon profitiert (OLG Karlsruhe). " +
		"Verstöße kosten 75 € und einen Punkt in Flensburg. " +
		"Diese Angaben sind nur zur Planung vor Fahrtantritt oder während eines Halts bestimmt. " +
		"Die Standorte stammen aus amtlichen und offenen Quellen und können unvollständig oder veraltet sein; mobile Kontrollen sind jederzeit und überall möglich. " +
		"Halten Sie sich immer an die geltende Höchstgeschwindigkeit. " +
		"Keine Rechtsberatung."
	speedNoticeEN = "Note on use (§ 23 Abs. 1c StVO): a person driving a vehicle may not, while driving, operate or use any technical device or app function that shows or disrupts traffic enforcement. " +
		"This also applies to a passenger's smartphone if the driver benefits from it (OLG Karlsruhe). " +
		"A violation costs €75 and one point in Flensburg. " +
		"This information is meant only for planning before setting off or during a stop. " +
		"The locations come from official and open sources and may be incomplete or out of date; mobile checks are possible at any time and anywhere. " +
		"Always keep to the speed limit in force. " +
		"Not legal advice."
)

// SpeedNotice is the § 23 Abs. 1c StVO notice in language: English for
// "en", the German original otherwise.
func SpeedNotice(language string) string {
	if language == "en" {
		return speedNoticeEN
	}
	return speedNoticeDE
}
