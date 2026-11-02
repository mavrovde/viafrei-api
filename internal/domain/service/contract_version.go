// Package service holds the pure domain rules of the service operations:
// health, readiness and the read-contract compatibility rule. It imports the
// standard library only.
package service

import (
	"fmt"
	"strconv"
	"strings"
)

// ContractVersion is a read-contract version in major.minor form.
type ContractVersion struct {
	Major int
	Minor int
}

// ParseContractVersion parses "major.minor".
func ParseContractVersion(s string) (ContractVersion, error) {
	major, minor, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok {
		return ContractVersion{}, fmt.Errorf("contract version %q is not major.minor", s)
	}
	ma, err1 := strconv.Atoi(major)
	mi, err2 := strconv.Atoi(minor)
	if err1 != nil || err2 != nil || ma < 0 || mi < 0 {
		return ContractVersion{}, fmt.Errorf("contract version %q is not major.minor", s)
	}
	return ContractVersion{Major: ma, Minor: mi}, nil
}

// String renders the version as major.minor.
func (v ContractVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Accepts reports whether a database publishing version got can serve a
// service that requires v: the same major, and a minor at least as new
// (VER-009). A minor bump only adds, so a newer minor is compatible.
func (v ContractVersion) Accepts(got ContractVersion) bool {
	return got.Major == v.Major && got.Minor >= v.Minor
}
