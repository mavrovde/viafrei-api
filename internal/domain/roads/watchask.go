package roads

import (
	"regexp"
	"slices"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// WatchAsk is what a road or place watch's request asks, as the platform
// reads the person's words: a jam only, a jam beside something a watch
// reports, or no jam at all.
type WatchAsk string

// The asks of a watch request.
const (
	AskJamOnly         WatchAsk = "jam_only"
	AskJamAndWatchable WatchAsk = "jam_and_watchable"
	AskPlain           WatchAsk = "plain"
)

// jamWords are the platform's word forms of traffic flow, whole words or
// phrases after the name fold (presentation/road-watch-scope.ts).
var jamWords = []string{"stau", "staus", "staue", "stauen", "staut", "stauende", "stauendes", "staugefahr", "staubildung", "stauwarnung", "staumeldung", "staumeldungen", "stauprognose", "staulange", "stauung", "stauungen", "ruckstau", "ruckstaus", "rueckstau", "rueckstaus", "dauerstau", "verkehrsstau", "verkehrsstaus", "berufsverkehrsstau", "stockend", "stockender", "stockenden", "stockendem", "stockendes", "stockt", "zahfliessend", "zahfliessender", "zahfliessenden", "zahfliessendem", "zahfliessendes", "zaehfliessend", "zaehfliessender", "zaehfliessenden", "zaehfliessendem", "zaehfliessendes", "zah fliessend", "zah fliessender", "zah fliessenden", "zaeh fliessend", "zaeh fliessender", "zaeh fliessenden", "verzogerung", "verzogerungen", "verzoegerung", "verzoegerungen", "langsamer verkehr", "langsamen verkehr", "langsamem verkehr", "langsam fliessend", "langsam fliessender", "stop and go", "stop go", "jam", "jams", "jammed", "traffic jam", "traffic jams", "congestion", "congested", "queue", "queues", "queuing", "queueing", "tailback", "tailbacks", "slow traffic", "slow moving", "slow moving traffic", "slowdown", "slowdowns", "slow down", "slows down", "delay", "delays", "delayed", "gridlock", "gridlocked", "backed up", "backup", "backups", "traffic build up", "traffic builds up", "traffic building up", "traffic buildup"}

var (
	jamAsk = func() *regexp.Regexp {
		w := slices.Clone(jamWords)
		slices.SortStableFunc(w, func(a, b string) int { return len(b) - len(a) })
		for i := range w {
			w[i] = regexp.QuoteMeta(w[i])
		}
		return regexp.MustCompile(`(^| )(` + strings.Join(w, "|") + `|[a-z]+staus?|slow)( |$)`)
	}()
	watchableAsk = regexp.MustCompile(`(^| )([a-z]*sperr[a-z]*|gesperrt|closure[a-z]*|closed|closing|reopen[a-z]*|re open[a-z]*|open again|wieder (frei|offen|befahrbar)|freigegeben|freigabe|[a-z]*einschrank[a-z]*|[a-z]*einschraenk[a-z]*|restrict[a-z]*|[a-z]*baustell[a-z]*|roadwork[a-z]*|road work[a-z]*|unfall[a-z]*|accident[a-z]*|block[a-z]*|blockier[a-z]*|lane[a-z]*|fahrstreifen[a-z]*)( |$)`)
)

// AskOf reads a watch request as the platform does: the request, and for a
// road watch its key too ("Stau A7"); a place key is never read.
func AskOf(road bool, request, key string) WatchAsk {
	texts := []string{request}
	if road {
		texts = append(texts, key)
	}
	var folded []string
	for _, t := range texts {
		if f := places.NameFold(t); f != "" {
			folded = append(folded, f)
		}
	}
	all := strings.Join(folded, " ")
	if !jamAsk.MatchString(all) {
		return AskPlain
	}
	if watchableAsk.MatchString(all) {
		return AskJamAndWatchable
	}
	return AskJamOnly
}

// WatchEvents are the events a road watch watches, as the platform infers
// them when the condition names none: a jam only, jams beside the closures
// and restrictions, or none named.
func WatchEvents(explicit []string, ask WatchAsk) []string {
	if explicit != nil {
		return explicit
	}
	switch ask {
	case AskJamOnly:
		return []string{"jams"}
	case AskJamAndWatchable:
		return []string{"restrictions", "jams"}
	}
	return nil
}
