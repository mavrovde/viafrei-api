package console

import (
	"bytes"
	"html/template"
	"net/http"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// view is every page's data. A page never carries a MAC, a pepper or a
// client address; the full key is carried only by the page that answers an
// issue or a rotation, once.
type view struct {
	Title, Message string
	Session        *console.Session
	Keys           []auth.KeyView
	Plans          []access.Plan
	Passkeys       []console.Passkey
	Audit          []auth.AuditEntry
	Confirm        *pendingChange
	ConfirmID      string
	KeyID, Key     string
	Actions        []actionForm
}

var funcs = template.FuncMap{
	"when": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") },
	"whenp": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04 UTC")
	},
	"fingerprint": console.Fingerprint,
}

const layout = `{{define "top"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer"><title>{{.Title}} · ViaFrei API console</title>
<link rel="stylesheet" href="/console.css"></head><body>
<header><strong>ViaFrei API console</strong>{{if .Session}}
<nav><a href="/">Keys</a> <a href="/audit">Audit</a>
<form method="post" action="/signout"><input type="hidden" name="csrf" value="{{.Session.CSRF}}"><button>Sign out {{.Session.Owner}}</button></form></nav>{{end}}
</header><main><h1>{{.Title}}</h1>{{end}}
{{define "bottom"}}</main></body></html>{{end}}

{{define "message"}}{{template "top" .}}<p>{{.Message}}</p><p><a href="/">Back to the console</a></p>{{template "bottom" .}}{{end}}

{{define "signin"}}{{template "top" .}}
<p>Sign in with your passkey. Your device asks you to verify yourself.</p>
<button id="signin" type="button">Sign in with a passkey</button>
<p id="status" role="status"></p>
<p><a href="/enrol">Enrol a passkey with an enrolment code</a></p>
<script src="/console.js"></script>{{template "bottom" .}}{{end}}

{{define "enrol"}}{{template "top" .}}
<p>Enter the one-time code that <code>viafrei-api console enrol</code> printed, then create the passkey.</p>
<label>Enrolment code <input id="code" autocomplete="off" spellcheck="false" maxlength="64"></label>
<button id="enrol" type="button">Create the passkey</button>
<p id="status" role="status"></p>
<script src="/console.js"></script>{{template "bottom" .}}{{end}}

{{define "home"}}{{template "top" .}}
<h2>Keys</h2>
<table><thead><tr><th>Key id</th><th>Environment</th><th>Plan</th><th>Label</th><th>Contact</th><th>Issued</th><th>Expires</th><th>Revoked</th></tr></thead><tbody>
{{range .Keys}}<tr><td><code>{{.ID}}</code></td><td>{{.Environment}}</td><td>{{.Plan}}</td><td>{{.Label}}</td><td>{{.Contact}}</td><td>{{when .IssuedAt}}</td><td>{{when .ExpiresAt}}</td><td>{{whenp .RevokedAt}} {{.RevokeReason}}</td></tr>
{{else}}<tr><td colspan="8">No keys.</td></tr>{{end}}</tbody></table>
<h2>Plans</h2>
<table><thead><tr><th>Plan</th><th>Use class</th><th>Tier</th><th>Scopes</th><th>Sources</th><th>Rate</th><th>Burst</th><th>Quota a day</th><th>Quota a month</th></tr></thead><tbody>
{{range .Plans}}<tr><td>{{.Name}}</td><td>{{.UseClass}}</td><td>{{.Tier}}</td><td>{{range .Scopes}}{{.}} {{end}}</td><td>{{range .Sources}}{{.}} {{end}}</td><td>{{.Rate}}</td><td>{{.Burst}}</td><td>{{.QuotaDay}}</td><td>{{.QuotaMonth}}</td></tr>
{{else}}<tr><td colspan="9">No plans.</td></tr>{{end}}</tbody></table>
<h2>Passkeys</h2>
<table><thead><tr><th>Owner</th><th>Passkey</th><th>Enrolled</th><th>Last used</th></tr></thead><tbody>
{{range .Passkeys}}<tr><td>{{.Owner}}</td><td><code>{{fingerprint .ID}}</code></td><td>{{when .CreatedAt}}</td><td>{{whenp .LastUsedAt}}</td></tr>{{end}}</tbody></table>
<h2>Changes</h2>
<p>Every change asks for a confirmation before it is made, and is audited with you as the actor.</p>
{{$csrf := .Session.CSRF}}{{range .Actions}}<form method="post" action="/act/{{.Action}}" class="action"><fieldset><legend>{{.Title}}</legend>
<input type="hidden" name="csrf" value="{{$csrf}}">
{{range .Fields}}<label>{{.Label}} <input name="{{.Name}}" {{if .Required}}required{{end}} maxlength="200"></label>{{end}}
<button>{{.Title}}…</button></fieldset></form>
{{end}}{{template "bottom" .}}{{end}}

{{define "confirm"}}{{template "top" .}}
<p>Confirm this change. Nothing has been changed yet.</p>
<table><tbody><tr><th>Change</th><td>{{.Confirm.Title}}</td></tr>
{{range .Confirm.Fields}}<tr><th>{{.Label}}</th><td>{{.Value}}</td></tr>{{end}}</tbody></table>
<form method="post" action="/confirm"><input type="hidden" name="csrf" value="{{.Session.CSRF}}"><input type="hidden" name="id" value="{{.ConfirmID}}">
<button>Confirm: {{.Confirm.Title}}</button></form>
<p><a href="/">Cancel</a></p>{{template "bottom" .}}{{end}}

{{define "result"}}{{template "top" .}}<p>{{.Message}}</p>
{{if .Key}}<p>Key id <code>{{.KeyID}}</code></p><p><code class="secret">{{.Key}}</code></p>
<p><strong>This is the only time the key is shown. Store it now.</strong></p>{{end}}
<p><a href="/">Back to the console</a></p>{{template "bottom" .}}{{end}}

{{define "audit"}}{{template "top" .}}
<table><thead><tr><th>At</th><th>Action</th><th>Key id</th><th>Plan before</th><th>Plan after</th><th>Actor</th><th>Reason</th></tr></thead><tbody>
{{range .Audit}}<tr><td>{{when .At}}</td><td>{{.Action}}</td><td><code>{{.KeyID}}</code></td><td>{{.PlanBefore}}</td><td>{{.PlanAfter}}</td><td>{{.Actor}}</td><td>{{.Reason}}</td></tr>
{{else}}<tr><td colspan="7">No audit records.</td></tr>{{end}}</tbody></table>{{template "bottom" .}}{{end}}`

var pages = template.Must(template.New("console").Funcs(funcs).Parse(layout))

// render writes a page. The page is rendered into a buffer first, so a page
// that fails renders nothing of itself.
func (s *server) render(w http.ResponseWriter, status int, name string, v view) {
	var b bytes.Buffer
	invariant.Check(pages.ExecuteTemplate(&b, name, v))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}
