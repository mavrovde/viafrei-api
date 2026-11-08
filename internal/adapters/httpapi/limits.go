package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
)

// RateLimitDraft is the revision of the IETF RateLimit header fields draft
// whose syntax the RateLimit-Policy and RateLimit fields follow (RATE-006).
const RateLimitDraft = "draft-ietf-httpapi-ratelimit-headers-09"

// The nominal windows of the day and month policies, in seconds: a quota
// day is 23 or 25 hours long across a daylight-saving change, and t of the
// RateLimit field gives the exact time to the reset.
const (
	dayWindow   = 86400
	monthWindow = 2592000
)

// KeyGate is the port of the per-key bucket and quotas.
type KeyGate interface {
	Admit(ctx context.Context, key string, p ratelimit.Plan) ratelimit.Verdict
}

// AddressGate is the port of the per-address bucket.
type AddressGate interface {
	Take(client string) limits.Decision
}

// Meter is the port of the usage metering.
type Meter interface {
	Record(e metering.Event)
}

type clientKey struct{}

// clientFrom returns the client address of the request: set by withClient.
func clientFrom(ctx context.Context) string {
	c, _ := ctx.Value(clientKey{}).(string)
	return c
}

// withClient records the client address: the immediate peer, or, when the
// peer is one of server.trusted_proxies, the address the proxy names last in
// X-Forwarded-For (RATE-001). The address is held for the request only.
func withClient(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			client := peer(r.RemoteAddr)
			if isTrusted(client, trusted) {
				if fwd, ok := parseForwarded(r.Header.Values("X-Forwarded-For")); ok {
					client = fwd
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, client)))
		})
	}
}

// peer is the host of a remote address, or the whole value when it has no
// port.
func peer(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

func isTrusted(client string, trusted []netip.Prefix) bool {
	a, err := netip.ParseAddr(client)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseForwarded returns the last address of X-Forwarded-For: the one the
// trusted proxy itself appended. Any earlier entry was written by the
// client and proves nothing. ok is false when the last entry is no address.
func parseForwarded(values []string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	list := strings.Split(values[len(values)-1], ",")
	a, err := netip.ParseAddr(strings.TrimSpace(list[len(list)-1]))
	if err != nil {
		return "", false
	}
	return a.Unmap().String(), true
}

// TrustedProxies parses server.trusted_proxies; the declaration has already
// validated each entry as a CIDR.
func TrustedProxies(cidrs []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}

// limitAddress is gate 1 (RATE-001, SEC-051): a request that presents no
// key, a service operation's included, takes a token of its client
// address's bucket before anything else is done for it. A request that
// presents a key is governed by its key's bucket instead, and by the
// per-address limit on failed key checks when its key is refused (SEC-052).
func limitAddress(gate AddressGate) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gate != nil && r.Header.Get("Authorization") == "" && r.Header.Get(APIKeyHeader) == "" {
				if d := gate.Take(clientFrom(r.Context())); !d.Allowed {
					writeProblem(w, newProblem(r.Context(), SlugRateLimited, "Too many requests without a key from this address.", limits.Seconds(d.RetryAfter)))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// outcome is what the storage reads of a keyed request report back for its
// usage record.
type outcome struct {
	refused metering.Refusal
}

type outcomeKey struct{}

func noteRefusal(ctx context.Context, r metering.Refusal) {
	if o, ok := ctx.Value(outcomeKey{}).(*outcome); ok {
		o.refused = r
	}
}

// planOf is the part of a key's plan the key gates read.
func planOf(p access.Plan) ratelimit.Plan {
	return ratelimit.Plan{
		Rate:  limits.Rate{Per: p.Rate, Burst: p.Burst},
		Quota: limits.Quota{Day: p.QuotaDay, Month: p.QuotaMonth},
	}
}

// rateLimitFields renders the RateLimit-Policy and RateLimit fields of a
// verdict in the syntax of RateLimitDraft: one policy for the burst, and
// one for each quota the plan has (RATE-006).
func rateLimitFields(p ratelimit.Plan, v ratelimit.Verdict) (policy, state string) {
	itoa := strconv.FormatInt
	pol := []string{`"burst";q=` + strconv.Itoa(p.Rate.Burst) + `;w=` + strconv.Itoa(limits.Seconds(p.Rate.Window()))}
	st := []string{`"burst";r=` + strconv.Itoa(v.Burst.Remaining) + `;t=` + strconv.Itoa(limits.Seconds(v.Burst.Reset))}
	if p.Quota.Day > 0 {
		pol = append(pol, `"day";q=`+itoa(p.Quota.Day, 10)+`;w=`+strconv.Itoa(dayWindow))
		st = append(st, `"day";r=`+itoa(v.DayLeft, 10)+`;t=`+strconv.Itoa(limits.Seconds(v.DayReset)))
	}
	if p.Quota.Month > 0 {
		pol = append(pol, `"month";q=`+itoa(p.Quota.Month, 10)+`;w=`+strconv.Itoa(monthWindow))
		st = append(st, `"month";r=`+itoa(v.MonthLeft, 10)+`;t=`+strconv.Itoa(limits.Seconds(v.MonthReset)))
	}
	return strings.Join(pol, ", "), strings.Join(st, ", ")
}

// keyGates runs gates 2 and 3 for an authenticated request, sets the
// RateLimit fields, and returns the problem of a refusal (nil when the
// request passes) with the refusal's reason.
func (s *server) keyGates(ctx huma.Context, principal access.Record) (*Problem, metering.Refusal) {
	if s.keyGate == nil {
		return nil, ""
	}
	p := planOf(principal.Plan)
	v := s.keyGate.Admit(ctx.Context(), principal.ID, p)
	if s.cfg.RateLimit.Headers {
		policy, state := rateLimitFields(p, v)
		ctx.SetHeader("RateLimit-Policy", policy)
		ctx.SetHeader("RateLimit", state)
	}
	switch v.Refused {
	case ratelimit.RateLimited:
		return newProblem(ctx.Context(), SlugRateLimited, "The key's request rate is exceeded.", limits.Seconds(v.RetryAfter)), metering.RateLimited
	case ratelimit.QuotaExceeded:
		return newProblem(ctx.Context(), SlugQuotaExceeded, "The key's quota is used up until the reset.", limits.Seconds(v.RetryAfter)), metering.QuotaExceeded
	}
	return nil, ""
}

// meter records the usage of one keyed request after its answer.
func (s *server) meter(ctx huma.Context, key string, refused metering.Refusal) {
	if s.metering == nil {
		return
	}
	s.metering.Record(metering.Event{
		Key: key, Operation: ctx.Operation().OperationID, NotModified: ctx.Status() == http.StatusNotModified,
		Refused: refused, Storage: admission.StorageTime(ctx.Context()),
	})
}

// overloadedProblem is the answer to a read the admission shed.
func (s *server) overloadedProblem(ctx context.Context, err error) (*Problem, bool) {
	var r *admission.Refusal
	if !errors.As(err, &r) {
		return nil, false
	}
	noteRefusal(ctx, metering.Overloaded)
	p := newProblem(ctx, SlugOverloaded, "The service is overloaded; retry after the time given.", limits.Seconds(r.RetryAfter))
	p.Status = s.cfg.Admission.ShedStatus
	return p, true
}
