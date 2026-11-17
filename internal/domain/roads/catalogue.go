package roads

// CatalogueQuery asks for the motorways: all of them, those of one Land, or
// one road with its junctions.
type CatalogueQuery struct {
	State string
	Road  string
	Limit int
}

// Motorway is one motorway of the catalogue.
type Motorway struct {
	Road      string
	Name      string
	LengthKm  *float64
	States    []string
	Junctions int
	// Situations counts its situations in force now, not withdrawn.
	Situations int
	Family     string
	// Exits are its junctions in number order, when one road was asked for.
	Exits []Junction
}

// Junction is one numbered junction of a motorway.
type Junction struct {
	ID, Name, Kind string
	Number         *string
	State          *string
	Lat, Lon       float64
}
