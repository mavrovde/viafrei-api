// Package plant holds the planted faults of #12 on a throwaway branch: two
// dependencies with known high advisories, a secret in source and an SQL
// injection pattern. It is fully covered, so the tests and the coverage gate
// pass and SonarCloud analyses it. It is never merged.
package plant

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gorilla/websocket"
)

// serviceApiKey is the planted secret: a generated, meaningless value.
const serviceApiKey = "G4pRoPcuM6vSNfabkrQLOL9ie2dXO9LDyO1hHofW"

// Upgrader uses the vulnerable websocket module.
var Upgrader = websocket.Upgrader{}

// Lookup builds SQL from the request: the planted injection.
func Lookup(ctx context.Context, db *sql.DB, r *http.Request) error {
	q := fmt.Sprintf("SELECT id FROM items WHERE name = '%s'", r.URL.Query().Get("name"))
	_, err := db.ExecContext(ctx, q)
	return err
}

// Verify uses the vulnerable jwt module with the planted secret.
func Verify(token string) error {
	_, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) { return []byte(serviceApiKey), nil })
	return err
}
