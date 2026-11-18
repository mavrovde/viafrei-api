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
	// Registers are the families of the junctions its junction count and
	// Länder are read from (the motorway location table).
	Registers []string
	// Sources are the families of the situations it counts, the families
	// an answer listing it credits.
	Sources []string
	// Exits are its junctions in number order, when one road was asked for.
	Exits []Junction
	// Lamp is its motorway Ampel now, as its own conditions answer states
	// it (LampOf over every situation on it and the warnings it crosses).
	Lamp Lamp
}

// Junction is one numbered junction of a motorway.
type Junction struct {
	ID, Name, Kind string
	Number         *string
	State          *string
	Lat, Lon       float64
}
