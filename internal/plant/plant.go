// Package plant holds planted faults for the throwaway branch that proves
// the checks of #5 and #12 go red. It is never merged.
package plant

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gorilla/websocket"
)

// Planted secret (a generated, meaningless value).
const serviceApiKey = "GwS8QWzm3qZAQpThe43rLwkJRX69iIpsVW1theV3"

var upgrader = websocket.Upgrader{}

// Handler builds SQL from the request: the planted injection.
func Handler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		rows, err := db.Query(fmt.Sprintf("SELECT id FROM items WHERE name = '%s'", name))
		if err == nil {
			_ = rows.Close()
		}
		_, _ = jwt.Parse(r.Header.Get("Authorization"), func(*jwt.Token) (interface{}, error) { return []byte(serviceApiKey), nil })
		if c, err := upgrader.Upgrade(w, r, nil); err == nil {
			_ = c.Close()
		}
	}
}

// Cleanup drops an error: the planted lint fault (errcheck).
func Cleanup() {
	os.Remove("planted.tmp")
}
