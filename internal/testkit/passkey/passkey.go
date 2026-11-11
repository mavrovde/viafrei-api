// Package passkey is a software WebAuthn authenticator for tests: it answers
// a relying party's creation and request options the way a browser and a
// platform authenticator would, with "none" attestation and an ES256 key.
// Its fields let a test break one property at a time (the origin, user
// verification, the counter) to drive each refusal.
package passkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/fxamacker/cbor/v2"

	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// Flags of the authenticator data.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

// Authenticator holds one credential.
type Authenticator struct {
	// Origin is what the client data reports; RPID is hashed into the
	// authenticator data.
	Origin, RPID string
	// SkipUV leaves the user-verified flag unset.
	SkipUV bool
	// Counter is the signature counter of the next answer; it is
	// incremented after each one.
	Counter uint32
	// ID is the credential id; Handle the user handle the relying party
	// gave at creation.
	ID, Handle []byte

	key *ecdsa.PrivateKey
}

// New returns an authenticator with a fresh key and credential id.
func New(origin, rpID string) *Authenticator {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Authenticator{
		Origin: origin, RPID: rpID, Counter: 1, ID: id,
		key: invariant.Must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader)),
	}
}

var b64 = base64.RawURLEncoding

// options reads publicKey.challenge and publicKey.user.id from options JSON
// of the shape {"publicKey": {...}}.
type options struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

func (a *Authenticator) clientData(typ string, raw json.RawMessage) ([]byte, options) {
	var o options
	invariant.Check(json.Unmarshal(raw, &o))
	return invariant.Must(json.Marshal(map[string]any{
		"type": typ, "challenge": o.PublicKey.Challenge, "origin": a.Origin, "crossOrigin": false,
	})), o
}

func (a *Authenticator) authData(extra []byte, flags byte) []byte {
	rp := sha256.Sum256([]byte(a.RPID))
	if !a.SkipUV {
		flags |= flagUV
	}
	out := append(rp[:], flags|flagUP)
	out = binary.BigEndian.AppendUint32(out, a.Counter)
	a.Counter++
	return append(out, extra...)
}

// Create answers creation options with a new credential, as the JSON a
// browser posts back.
func (a *Authenticator) Create(raw json.RawMessage) json.RawMessage {
	cd, o := a.clientData("webauthn.create", raw)
	a.Handle = invariant.Must(b64.DecodeString(o.PublicKey.User.ID))
	point := invariant.Must(a.key.PublicKey.Bytes()) // 0x04 || X || Y
	x, y := point[1:33], point[33:]
	cose := invariant.Must(cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y}))
	att := make([]byte, 16)                                     // AAGUID: none
	att = binary.BigEndian.AppendUint16(att, uint16(len(a.ID))) // #nosec G115 -- 16 bytes
	att = append(append(att, a.ID...), cose...)
	obj := invariant.Must(cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(att, flagAT)}))
	return invariant.Must(json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.ID), "rawId": b64.EncodeToString(a.ID), "type": "public-key",
		"response": map[string]string{"clientDataJSON": b64.EncodeToString(cd), "attestationObject": b64.EncodeToString(obj)},
	}))
}

// Get answers request options with an assertion of the credential, as the
// JSON a browser posts back.
func (a *Authenticator) Get(raw json.RawMessage) json.RawMessage {
	cd, _ := a.clientData("webauthn.get", raw)
	ad := a.authData(nil, 0)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig := invariant.Must(ecdsa.SignASN1(rand.Reader, a.key, digest[:]))
	return invariant.Must(json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.ID), "rawId": b64.EncodeToString(a.ID), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.Handle),
		},
	}))
}
