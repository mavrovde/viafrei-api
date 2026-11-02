package contract_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/db/contract"
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
