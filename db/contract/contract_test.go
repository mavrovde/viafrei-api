package contract_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestEmbeddedContract(t *testing.T) {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(contract.Version()) {
		t.Errorf("Version() = %q, want major.minor", contract.Version())
	}
	if !strings.Contains(contract.SchemaSQL(), "contract_info") {
		t.Error("SchemaSQL() does not build contract_info")
	}
	if !strings.Contains(contract.ReaderRoleSQL(), contract.ReaderRole) {
		t.Errorf("ReaderRoleSQL() does not name %s", contract.ReaderRole)
	}
}

// TestRoleLimitCoversPoolMax holds the reader role's connection limit and the
// largest pool the configuration accepts together: a pool at db.pool_max must
// never be refused a connection by the role (NFR-SEC-004, CFG-004).
func TestRoleLimitCoversPoolMax(t *testing.T) {
	req.Covers(t, "NFR-SEC-004", "CFG-004")
	m := regexp.MustCompile(`connection limit ([0-9]+)`).FindStringSubmatch(contract.ReaderRoleSQL())
	if m == nil {
		t.Fatal("ReaderRoleSQL() sets no connection limit")
	}
	limit, _ := strconv.Atoi(m[1])
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
	if poolMax > limit {
		t.Errorf("db.pool_max accepts up to %d connections, the reader role allows %d", poolMax, limit)
	}
}
