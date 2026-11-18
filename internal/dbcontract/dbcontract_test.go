package dbcontract_test

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestContractVersionIsMajorMinor(t *testing.T) {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(dbcontract.ContractVersion) {
		t.Errorf("ContractVersion = %q, want major.minor", dbcontract.ContractVersion)
	}
}

// TestRoleLimitCoversPoolMax holds the reader role's connection limit (the
// database release gives api_reader CONNECTION LIMIT 16; the store's
// integration test reads it from the role) and the largest pool the
// configuration accepts together: a pool at db.pool_max must never be refused
// a connection by the role (NFR-SEC-004, CFG-004).
func TestRoleLimitCoversPoolMax(t *testing.T) {
	req.Covers(t, "NFR-SEC-004", "CFG-004")
	poolMax := 0
	for _, f := range config.Fields() {
		if f.Key != "db.pool_max" {
			continue
		}
		b := regexp.MustCompile(`max=([0-9]+)`).FindStringSubmatch(f.Validate)
		if b == nil {
			t.Fatalf("db.pool_max declares no maximum: %q", f.Validate)
		}
		poolMax, _ = strconv.Atoi(b[1])
	}
	if poolMax < 1 {
		t.Fatal("db.pool_max is not declared")
	}
	if poolMax > dbcontract.ReaderConnectionLimit {
		t.Errorf("db.pool_max accepts up to %d connections, the reader role allows %d", poolMax, dbcontract.ReaderConnectionLimit)
	}
}
