package transit

import (
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// StateAlertRow is one service alert in force that names a stop, with the
// message it belongs to and the Länder its stops lie in.
type StateAlertRow struct {
	ID, Header string
	Until      *time.Time
	Lines      []string
	// MessageKey is the id of the message's row of the primary family where
	// one carries it: rows sharing it are one message.
	MessageKey string
	// CarriedBy are the families carrying the message, sorted.
	CarriedBy []string
	// Lands are the Länder of the districts its stops lie in.
	Lands []string
}

// StateAlert is one message of a Land's networks, as the platform's Land page
// tells it: the publisher's headline, every line it names, its end and the
// families that carry it.
type StateAlert struct {
	Header    string
	Lines     []string
	Until     *time.Time
	CarriedBy []string
}

// StateAlertsOf folds rows to one alert per message whose stops lie in land,
// as the platform's Land page does: the headline and end of the message's own
// row (the primary family's) where it is among the rows, else of its first;
// the lines and families of every row; the messages naming most lines first,
// then by headline in German order. Lines are in natural order.
func StateAlertsOf(rows []StateAlertRow, land string) []StateAlert {
	type message struct {
		alert StateAlert
		own   bool
		lines map[string]bool
		fams  map[string]bool
		lands map[string]bool
	}
	var order []string
	by := map[string]*message{}
	for _, r := range rows {
		own := r.ID == r.MessageKey
		m, ok := by[r.MessageKey]
		if !ok {
			m = &message{alert: StateAlert{Header: r.Header, Until: r.Until}, own: own, lines: map[string]bool{}, fams: map[string]bool{}, lands: map[string]bool{}}
			by[r.MessageKey] = m
			order = append(order, r.MessageKey)
		}
		if own && !m.own {
			m.alert.Header, m.alert.Until, m.own = r.Header, r.Until, true
		}
		for _, l := range r.Lines {
			m.lines[l] = true
		}
		for _, f := range r.CarriedBy {
			m.fams[f] = true
		}
		for _, l := range r.Lands {
			m.lands[l] = true
		}
	}
	natural := collate.New(language.German, collate.Numeric)
	german := collate.New(language.German)
	out := []StateAlert{}
	for _, k := range order {
		m := by[k]
		if !m.lands[land] {
			continue
		}
		a := m.alert
		a.Lines = keys(m.lines)
		slices.SortFunc(a.Lines, natural.CompareString)
		a.CarriedBy = keys(m.fams)
		slices.Sort(a.CarriedBy)
		out = append(out, a)
	}
	slices.SortStableFunc(out, func(p, q StateAlert) int {
		if d := len(q.Lines) - len(p.Lines); d != 0 {
			return d
		}
		return german.CompareString(p.Header, q.Header)
	})
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
