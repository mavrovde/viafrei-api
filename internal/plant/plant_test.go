package plant

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"testing"

	jwt "github.com/dgrijalva/jwt-go"
)

type fakeDriver struct{}
type fakeConn struct{}
type fakeStmt struct{}

func (fakeDriver) Open(string) (driver.Conn, error)         { return fakeConn{}, nil }
func (fakeConn) Prepare(string) (driver.Stmt, error)        { return fakeStmt{}, nil }
func (fakeConn) Close() error                               { return nil }
func (fakeConn) Begin() (driver.Tx, error)                  { return nil, errors.New("no transactions") }
func (fakeStmt) Close() error                               { return nil }
func (fakeStmt) NumInput() int                              { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return nil, errors.New("no rows") }

func init() { sql.Register("plantfake", fakeDriver{}) }

func TestLookup(t *testing.T) {
	db, err := sql.Open("plantfake", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Lookup(context.Background(), db, httptest.NewRequest("GET", "/?name=x", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestVerify(t *testing.T) {
	_ = Upgrader
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{}).SignedString([]byte(serviceApiKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(tok); err != nil {
		t.Fatal(err)
	}
}
