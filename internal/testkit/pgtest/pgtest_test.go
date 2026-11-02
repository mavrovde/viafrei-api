package pgtest

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func fixed(url string, err error) func(context.Context, ...string) (string, error) {
	return func(context.Context, ...string) (string, error) { return url, err }
}

func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestFinishReportsEachFailure(t *testing.T) {
	cases := map[string]func(context.Context, ...string) (string, error){
		"connection string": fixed("", errors.New("no port mapped")),
		"owner URL":         fixed("postgres://owner@%zz/contract", nil),
		"connect as owner":  fixed("postgres://owner:pw@"+closedPort(t)+"/contract?sslmode=disable", nil),
	}
	for want, connString := range cases {
		db := &DB{}
		err := db.finish(context.Background(), connString)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("finish = %v, want an error about %s", err, want)
		}
		if db.OwnerURL != "" || db.ReaderURL != "" {
			t.Errorf("%s: a failed finish left URLs behind", want)
		}
	}
}

func TestStopWithoutContainer(t *testing.T) {
	(&DB{}).Stop()
}
