package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// formField is one input of an action's form.
type formField struct {
	Name, Label string
	Required    bool
}

// actionForm is one change the console offers.
type actionForm struct {
	Action, Title string
	Fields        []formField
}

// actions are the console's changes: the key lifecycle of the keys command.
var actions = []actionForm{
	{"plan", "Create or change a plan", []formField{
		{"name", "Plan", true}, {"use_class", "Use class (consumer, non_commercial, commercial)", true},
		{"tier", "Tier (critical, standard, bulk)", true}, {"scopes", "Scopes, comma-separated", false},
		{"sources", "Source families, comma-separated", false}, {"rate", "Rate, requests a second", true},
		{"burst", "Burst, requests at once", true}, {"quota_day", "Quota a day, 0 is none", false},
		{"quota_month", "Quota a month, 0 is none", false},
	}},
	{"issue", "Issue a key", []formField{
		{"plan", "Plan", true}, {"label", "Label", true}, {"contact", "Contact", true},
		{"expires", "Lifetime, e.g. 8760h; empty is the default", false},
	}},
	{"rotate", "Rotate a key", []formField{{"id", "Key id", true}}},
	{"revoke", "Revoke a key", []formField{{"id", "Key id", true}, {"reason", "Reason", true}}},
	{"move", "Move a key to another plan", []formField{{"id", "Key id", true}, {"plan", "Plan", true}}},
	{"accept", "Record a terms acceptance", []formField{
		{"id", "Key id", true}, {"source", "Source family", true}, {"version", "Terms version", true},
	}},
	{"prune", "Prune audit and usage records past retention", nil},
}

func formFor(action string) (actionForm, bool) {
	for _, a := range actions {
		if a.Action == action {
			return a, true
		}
	}
	return actionForm{}, false
}

// shownField is one value of a pending change as the confirmation shows it.
type shownField struct{ Name, Label, Value string }

// pendingChange is a change asked for and not yet confirmed.
type pendingChange struct {
	Action, Title string
	Fields        []shownField
	values        map[string]string
}

// value is the asked-for value of the field name; every field of the form
// is held, an empty one as "".
func (p *pendingChange) value(name string) string { return p.values[name] }

func (s *server) home(w http.ResponseWriter, r *http.Request, sess console.Session) {
	ctx := r.Context()
	keys, err := s.Keys.KeyList(ctx)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	plans, err := s.Keys.PlanList(ctx)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	passkeys, err := s.Owners.Passkeys(ctx)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "home", view{Title: "Keys", Session: &sess, Keys: keys, Plans: plans, Passkeys: passkeys, Actions: actions})
}

func (s *server) audit(w http.ResponseWriter, r *http.Request, sess console.Session) {
	entries, err := s.Keys.AuditList(r.Context(), s.Settings.AuditPage)
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "audit", view{Title: fmt.Sprintf("Audit, newest %d", s.Settings.AuditPage), Session: &sess, Audit: entries})
}

// ask holds the asked-for change and answers its confirmation page.
func (s *server) ask(w http.ResponseWriter, r *http.Request, sess console.Session) {
	form, ok := formFor(r.PathValue("action"))
	if !ok {
		s.refuse(w, r, http.StatusNotFound, "no such change")
		return
	}
	change := &pendingChange{Action: form.Action, Title: form.Title, values: map[string]string{}}
	for _, f := range form.Fields {
		v := strings.TrimSpace(r.PostForm.Get(f.Name))
		if f.Required && v == "" {
			s.refuse(w, r, http.StatusBadRequest, f.Label+" is required")
			return
		}
		change.Fields = append(change.Fields, shownField{Name: f.Name, Label: f.Label, Value: v})
		change.values[f.Name] = v
	}
	if _, err := build(change); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Guard.PutConfirm(sess, change)
	if err != nil {
		s.busy(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "confirm", view{Title: "Confirm", Session: &sess, Confirm: change, ConfirmID: id})
}

// run makes a confirmed change as actor and returns what the result page
// says, and the key when one was issued or rotated.
type run func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error)

// build checks a change's values and returns the call that makes it.
func build(c *pendingChange) (run, error) {
	v := c.value
	switch c.Action {
	case "plan":
		p := access.Plan{Name: v("name"), UseClass: access.UseClass(v("use_class")), Tier: v("tier"), Sources: list(v("sources"))}
		for _, sc := range list(v("scopes")) {
			p.Scopes = append(p.Scopes, access.Scope(sc))
		}
		var err error
		if p.Rate, err = number("rate", v("rate")); err != nil {
			return nil, err
		}
		if p.Burst, err = number("burst", v("burst")); err != nil {
			return nil, err
		}
		day, err := number("quota_day", v("quota_day"))
		if err != nil {
			return nil, err
		}
		month, err := number("quota_month", v("quota_month"))
		if err != nil {
			return nil, err
		}
		p.QuotaDay, p.QuotaMonth = int64(day), int64(month)
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			return "Plan " + p.Name + " saved.", access.Key{}, k.PutPlan(ctx, p, actor)
		}, nil
	case "issue":
		var expiry time.Duration
		if e := v("expires"); e != "" {
			d, err := time.ParseDuration(e)
			if err != nil {
				return nil, fmt.Errorf("the lifetime %q is not a duration such as 8760h", e)
			}
			expiry = d
		}
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			key, err := k.Issue(ctx, v("plan"), v("label"), v("contact"), expiry, actor)
			return "Key issued.", key, err
		}, nil
	case "rotate":
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			key, err := k.Rotate(ctx, v("id"), actor)
			return "Key rotated; the previous secret stays valid for the rotation overlap.", key, err
		}, nil
	case "revoke":
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			return "Key " + v("id") + " revoked.", access.Key{}, k.Revoke(ctx, v("id"), v("reason"), actor)
		}, nil
	case "move":
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			return "Key " + v("id") + " moved to " + v("plan") + ".", access.Key{}, k.Move(ctx, v("id"), v("plan"), actor)
		}, nil
	case "accept":
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			return "Acceptance recorded.", access.Key{}, k.Accept(ctx, v("id"), v("source"), v("version"), actor)
		}, nil
	default: // prune: the forms above are the only actions ask admits.
		return func(ctx context.Context, k Lifecycle, actor string) (string, access.Key, error) {
			n, err := k.Prune(ctx, actor)
			if err != nil {
				return "", access.Key{}, err
			}
			u, err := k.PruneUsage(ctx, actor)
			return fmt.Sprintf("%d audit records and %d usage records removed.", n, u), access.Key{}, err
		}, nil
	}
}

func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// number reads an optional whole number; empty is 0.
func number(name, v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a whole number", name, v)
	}
	return n, nil
}

// confirm makes a change the same session asked for, once.
func (s *server) confirm(w http.ResponseWriter, r *http.Request, sess console.Session) {
	held, err := s.Guard.TakeConfirm(sess, r.PostForm.Get("id"))
	switch {
	case errors.Is(err, console.ErrExpired):
		s.refuse(w, r, http.StatusGone, "the confirmation has expired; ask for the change again")
		return
	case err != nil:
		s.refuse(w, r, http.StatusConflict, "unknown or already used confirmation")
		return
	}
	change := held.(*pendingChange)
	do, _ := build(change) // checked when the change was asked for
	message, key, err := do(r.Context(), s.Keys, console.ActorPrefix+sess.Owner)
	if err != nil {
		if errors.Is(err, auth.ErrInput) {
			s.refuse(w, r, http.StatusBadRequest, err.Error())
			return
		}
		s.Logger.Warn("console change refused", "action", change.Action, "owner", sess.Owner, "error", err.Error())
		s.refuse(w, r, http.StatusConflict, "the access store refused the change: "+publicReason(err))
		return
	}
	s.Logger.Info("console change", "action", change.Action, "owner", sess.Owner)
	s.render(w, http.StatusOK, "result", view{Title: change.Title, Session: &sess, Message: message, KeyID: key.ID, Key: key.Full})
}

// publicReason names a refusal without the store's own detail.
func publicReason(err error) string {
	if errors.Is(err, auth.ErrUnknown) {
		return "no such key"
	}
	return "see the server log"
}
