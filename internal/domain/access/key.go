// Package access is the domain of API keys: their format, the MAC stored in
// place of the secret, their lifecycle state, plans and scopes, and the
// licence gate that decides which source families a key may receive (Spec:
// Authentication and Authorisation). It has no I/O: storage and clocks are
// handed in.
package access

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/big"
	"strings"
)

// Environment is the instance kind a key belongs to (SEC-004).
type Environment string

// The two environments.
const (
	Live Environment = "live"
	Test Environment = "test"
)

// Prefixes are the configured key prefixes of both environments.
type Prefixes struct {
	Live, Test string
}

func (p Prefixes) of(env Environment) string {
	if env == Test {
		return p.Test
	}
	return p.Live
}

// Lengths of the parts of a key (Spec: Authentication and Authorisation,
// section 1).
const (
	IDLength       = 10
	SecretBytes    = 32
	SecretLength   = 43 // 32 bytes in base62, fixed width
	ChecksumLength = 6  // CRC-32 in base62, fixed width
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ErrMalformed is the answer to a string that is not a well-formed key of the
// expected environment: wrong prefix, wrong length, a character outside
// base62 or a checksum that does not match. It is never more specific, so a
// caller cannot probe which part failed.
var ErrMalformed = errors.New("access: not a well-formed key")

// Key is a freshly generated key. Full is shown once, at issue (SEC-012).
type Key struct {
	ID     string
	Secret string
	Full   string
}

// encode writes n in base62 with exactly width digits.
func encode(n *big.Int, width int) string {
	out := make([]byte, width)
	v := new(big.Int).Set(n)
	base, rem := big.NewInt(62), new(big.Int)
	for i := width - 1; i >= 0; i-- {
		v.QuoRem(v, base, rem)
		out[i] = base62[rem.Int64()]
	}
	return string(out)
}

func checksum(s string) string {
	return encode(new(big.Int).SetUint64(uint64(crc32.ChecksumIEEE([]byte(s)))), ChecksumLength)
}

// NewID returns a fresh public key id from r.
func NewID(r io.Reader) (string, error) {
	b := make([]byte, IDLength)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", fmt.Errorf("access: random source: %w", err)
	}
	for i := range b {
		b[i] = base62[int(b[i])%len(base62)]
	}
	return string(b), nil
}

// NewSecret returns a key for id with a fresh secret of 32 bytes from r.
func NewSecret(r io.Reader, env Environment, p Prefixes, id string) (Key, error) {
	b := make([]byte, SecretBytes)
	if _, err := io.ReadFull(r, b); err != nil {
		return Key{}, fmt.Errorf("access: random source: %w", err)
	}
	secret := encode(new(big.Int).SetBytes(b), SecretLength)
	body := p.of(env) + id + "_" + secret
	return Key{ID: id, Secret: secret, Full: body + checksum(body)}, nil
}

// Generate returns a new key with a fresh id and secret from r.
func Generate(r io.Reader, env Environment, p Prefixes) (Key, error) {
	id, err := NewID(r)
	if err != nil {
		return Key{}, err
	}
	return NewSecret(r, env, p, id)
}

func isBase62(s string) bool {
	for i := range len(s) {
		if !strings.ContainsRune(base62, rune(s[i])) {
			return false
		}
	}
	return true
}

// Parse checks a presented key against the format of env, its checksum
// included, without any store lookup (SEC-003), and returns its id and
// secret.
func Parse(s string, env Environment, p Prefixes) (id, secret string, err error) {
	prefix := p.of(env)
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) != IDLength+1+SecretLength+ChecksumLength || rest[IDLength] != '_' {
		return "", "", ErrMalformed
	}
	id, secret, sum := rest[:IDLength], rest[IDLength+1:IDLength+1+SecretLength], rest[IDLength+1+SecretLength:]
	if !isBase62(id) || !isBase62(secret) || checksum(prefix+id+"_"+secret) != sum {
		return "", "", ErrMalformed
	}
	return id, secret, nil
}

// Carries reports whether s contains something that starts like a key of
// either environment: what the query-string check refuses (SEC-002).
func (p Prefixes) Carries(s string) bool {
	return strings.Contains(s, p.Live) || strings.Contains(s, p.Test)
}
