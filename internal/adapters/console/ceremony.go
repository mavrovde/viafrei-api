package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// owner is a console owner as the WebAuthn library sees one.
type owner struct {
	name   string
	handle []byte
	creds  []webauthn.Credential
}

func (o owner) WebAuthnID() []byte                         { return o.handle }
func (o owner) WebAuthnName() string                       { return o.name }
func (o owner) WebAuthnDisplayName() string                { return o.name }
func (o owner) WebAuthnCredentials() []webauthn.Credential { return o.creds }

// begun is a ceremony's first answer: the single-use id the browser hands
// back with its credential, and the options it passes to the authenticator.
type begun struct {
	ID      string `json:"id"`
	Options any    `json:"options"`
}

// finish is a ceremony's second request.
type finish struct {
	ID         string          `json:"id"`
	Credential json.RawMessage `json:"credential"`
}

// enrolState is what an enrolment ceremony holds between its two requests.
type enrolState struct {
	session webauthn.SessionData
	code    string
	owner   owner
}

// must2 is invariant.Must for a call with two results.
func must2[A, B any](a A, b B, err error) (A, B) {
	invariant.Check(err)
	return a, b
}

var errCeremony = errors.New("console: the ceremony failed")

// readJSON reads a JSON request body into v.
func readJSON(r *http.Request, v any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return errCeremony
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// attempt counts one attempt and answers 429 over the limit.
func (s *server) attempt(w http.ResponseWriter, r *http.Request) bool {
	wait, err := s.Guard.Attempt()
	if err != nil {
		retryAfter(w, wait)
		s.refuse(w, r, http.StatusTooManyRequests, "too many sign-in attempts; try again later")
		return false
	}
	return true
}

// failed answers a failed ceremony. The failure counts as an attempt; the
// reason is logged, the answer says only that it failed.
func (s *server) failed(w http.ResponseWriter, r *http.Request, err error) {
	_, _ = s.Guard.Attempt()
	reason := "the passkey was not accepted"
	switch {
	case errors.Is(err, console.ErrExpired):
		reason = "the challenge has expired; start again"
	case errors.Is(err, console.ErrUnknown):
		reason = "unknown or already used challenge; start again"
	case errors.Is(err, console.ErrCode):
		reason = "unknown, used or expired enrolment code"
	}
	s.refuse(w, r, http.StatusUnauthorized, reason)
}

func (s *server) signInBegin(w http.ResponseWriter, r *http.Request) {
	if !s.attempt(w, r) {
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		s.refuse(w, r, http.StatusUnsupportedMediaType, "send application/json")
		return
	}
	// Beginning a ceremony fails only on a configuration New has accepted.
	options, session := must2(s.webauthn.BeginDiscoverableLogin())
	s.begin(w, r, "signin", *session, options)
}

// begin holds the ceremony's state under a new single-use id and answers it
// with the options.
func (s *server) begin(w http.ResponseWriter, r *http.Request, kind string, state any, options any) {
	id, err := s.Guard.PutChallenge(kind, state)
	if err != nil {
		s.busy(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, begun{ID: id, Options: options})
}

func (s *server) busy(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, console.ErrBusy) {
		retryAfter(w, s.Settings.ChallengeTTL)
		s.refuse(w, r, http.StatusServiceUnavailable, "too many pending ceremonies; try again shortly")
		return
	}
	s.unavailable(w, r, err)
}

// unavailable answers an internal failure without its detail.
func (s *server) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Error("console request failed", "path", r.URL.Path, "error", err.Error())
	s.refuse(w, r, http.StatusInternalServerError, "the console could not complete the request")
}

func (s *server) signInFinish(w http.ResponseWriter, r *http.Request) {
	var f finish
	if err := readJSON(r, &f); err != nil {
		s.failed(w, r, err)
		return
	}
	state, err := s.Guard.TakeChallenge(f.ID, "signin")
	if err != nil {
		s.failed(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(f.Credential)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	// The passkey the assertion names, held by the owner its user handle
	// names; the library then checks the assertion against it.
	key, err := s.Owners.Passkey(r.Context(), parsed.RawID)
	if err == nil && !bytes.Equal(key.UserHandle, parsed.Response.UserHandle) {
		err = errCeremony
	}
	if err != nil {
		s.failed(w, r, err)
		return
	}
	user := owner{name: key.Owner, handle: key.UserHandle, creds: []webauthn.Credential{{
		ID: key.ID, PublicKey: key.PublicKey, Flags: webauthn.CredentialFlagsFromMsgpByte(key.Flags),
		Authenticator: webauthn.Authenticator{SignCount: key.SignCount},
	}}}
	lookup := func([]byte, []byte) (webauthn.User, error) { return user, nil }
	_, cred, err := s.webauthn.ValidatePasskeyLogin(lookup, state.(webauthn.SessionData), parsed)
	if err == nil && cred.Authenticator.CloneWarning {
		err = errors.New("console: the signature counter went backwards")
	}
	if err != nil {
		s.Logger.Warn("console sign-in failed", "error", err.Error())
		s.failed(w, r, err)
		return
	}
	if err := s.Owners.SignedIn(r.Context(), key, cred.Authenticator.SignCount, cred.Flags.MsgpByte()); err != nil {
		s.unavailable(w, r, err)
		return
	}
	sess, err := s.Guard.StartSession(key.Owner)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: sess.Token, Path: "/", MaxAge: int(s.Settings.SessionTTL.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	s.Logger.Info("console sign-in", "owner", key.Owner, "passkey", console.Fingerprint(key.ID))
	writeJSON(w, http.StatusOK, map[string]string{"next": "/"})
}

func (s *server) enrolBegin(w http.ResponseWriter, r *http.Request) {
	if !s.attempt(w, r) {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		s.failed(w, r, err)
		return
	}
	e, err := s.Owners.Enrolment(r.Context(), req.Code)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	o := owner{name: e.Owner, handle: e.UserHandle}
	if o.handle == nil {
		o.handle = make([]byte, 32)
		if _, err := io.ReadFull(s.Random, o.handle); err != nil {
			s.unavailable(w, r, err)
			return
		}
	}
	options, session := must2(s.webauthn.BeginRegistration(o))
	s.begin(w, r, "enrol", enrolState{session: *session, code: req.Code, owner: o}, options)
}

func (s *server) enrolFinish(w http.ResponseWriter, r *http.Request) {
	var f finish
	if err := readJSON(r, &f); err != nil {
		s.failed(w, r, err)
		return
	}
	held, err := s.Guard.TakeChallenge(f.ID, "enrol")
	if err != nil {
		s.failed(w, r, err)
		return
	}
	state := held.(enrolState)
	parsed, err := protocol.ParseCredentialCreationResponseBytes(f.Credential)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	cred, err := s.webauthn.CreateCredential(state.owner, state.session, parsed)
	if err != nil {
		s.Logger.Warn("console enrolment failed", "error", err.Error())
		s.failed(w, r, err)
		return
	}
	p := console.Passkey{
		ID: cred.ID, Owner: state.owner.name, UserHandle: state.owner.handle, PublicKey: cred.PublicKey,
		SignCount: cred.Authenticator.SignCount, Flags: cred.Flags.MsgpByte(),
	}
	if err := s.Owners.Enrol(r.Context(), state.code, p); err != nil {
		if errors.Is(err, console.ErrCode) {
			s.failed(w, r, err)
			return
		}
		s.unavailable(w, r, err)
		return
	}
	s.Logger.Info("console passkey enrolled", "owner", p.Owner, "passkey", console.Fingerprint(p.ID))
	writeJSON(w, http.StatusOK, map[string]string{"next": "/signin"})
}
