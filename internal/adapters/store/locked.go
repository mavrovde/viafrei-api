package store

// FeatureTables are the platform's tables the feature reads (places,
// charging, parking, and the regions they are placed in) read, and
// ParkingTables those of the parking reads alone: a lock on the first hangs
// every storage read of a feature request, a lock on the second only the
// parking part (the load run's hanging query holds them).
var (
	FeatureTables = append([]string{"charging_sites", "charging_point_status", "charging_points", "ags_regions", "gazetteer_places"},
		ParkingTables...)
	ParkingTables = []string{"parking_facilities", "parking_facilities_odbl", "parking_facilities_bysa", "parking_facilities_sid",
		"parking_occupancy", "parking_occupancy_odbl", "parking_occupancy_sid"}
)
