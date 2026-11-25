//go:build integration

package store

import (
	"regexp"
	"strconv"
)

// legacyParam is a parameter of a frozen statement.
var legacyParam = regexp.MustCompile(`\$[0-9]+`)

func init() {
	// Several stops' boards at once, as the platform reads each stop's
	// (api_board_calls' frozen statement, its $1 the stop, $2 and $3 the
	// window, $4 the families): each call is the first listed stop's whose
	// board holds it.
	one := legacyParam.ReplaceAllStringFunc(legacyReads["api_board_calls"], func(p string) string {
		n, _ := strconv.Atoi(p[1:])
		return [...]string{"", "g.root", "$3", "$4", "$5"}[n]
	})
	legacyReads["api_stop_boards_calls"] = `
select distinct on (b.journey_id, b.stop_point_id, b.call_order) g.ord, b.*
from unnest($1::text[], $2::int[]) g(root, ord)
cross join lateral (` + one + `) b
order by b.journey_id, b.stop_point_id, b.call_order, g.ord`
}
