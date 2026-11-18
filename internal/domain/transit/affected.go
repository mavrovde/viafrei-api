package transit

// Affected is a station and what affects it now (the site's station board):
// its departures of the next hour cancelled and late, the service alerts in
// force that name it, and its lifts and escalators out of service.
type Affected struct {
	Station   Station
	Cancelled int
	Late      int
	Alerts    int
	LiftsOut  int
	// Families are the families of the figures counted, the station's own
	// first.
	Families []string
}
