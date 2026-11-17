package energy

import "errors"

// ErrNotFound is a filling station or charging site no permitted family
// holds, or one withdrawn.
var ErrNotFound = errors.New("energy: no such station or site")

// Address is a station's published postal address; each part nil when not
// published.
type Address struct {
	Street, HouseNumber, Postcode, City *string
}

// FuelStationPage is one filling station read by its id, as the site's
// station page and its price block state it: the station with its address
// and its newest price per grade inside the held window (fuel.price_held).
type FuelStationPage struct {
	FuelStation
	Address Address
}
