package accessstore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func cfgWith(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Access.URL = config.NewSecret(url)
	return cfg
}

func TestPoolConfig(t *testing.T) {
	pc, err := accessstore.PoolConfig(cfgWith(t, "postgres://a:pw@db.invalid:6000/x"))
	if err != nil || pc.MaxConns != 2 || pc.ConnConfig.Host != "db.invalid" {
		t.Fatalf("%v %v", pc, err)
	}
	_, err = accessstore.Open(context.Background(), cfgWith(t, "postgres://a:secretpw@h:notaport/x"))
	if err == nil || strings.Contains(err.Error(), "secretpw") {
		t.Fatalf("bad URL: %v", err)
	}
	s, err := accessstore.Open(context.Background(), cfgWith(t, "postgres://a:pw@db.invalid:6000/x"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
