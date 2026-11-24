//go:build integration

package store

// No view publishes the folded names of the districts and Gemeinden a town
// is read by (fault 2 of the 1.1.2 faults: "München Marienplatz" answered
// nothing): api_station_town_kreise reads the platform's tables directly, and
// is frozen as written.
func init() {
	legacyReads["api_station_town_kreise"] = stationTownKreise.SQL
}
