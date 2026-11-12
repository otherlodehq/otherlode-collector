// Package ratelimit throttles requests per client IP as HTTP middleware.
package ratelimit

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

const defaultStaleAfter = 10 * time.Minute

const defaultMaxVisitors = 100_000

const maxTextKey = 64

// DefaultIPv6Prefix keys each IPv6 address on its own.
const DefaultIPv6Prefix = 128

// Limiter throttles requests per client IP with a token bucket per key. The
// zero value is not usable; construct with New.
type Limiter struct {
	rate  rate.Limit
	burst int

	clientIPHeader string

	staleAfter time.Duration

	maxVisitors int

	ipv6Prefix int

	mu       sync.Mutex
	visitors map[string]*list.Element
	order    *list.List

	stop     chan struct{}
	stopOnce sync.Once
}

type visitor struct {
	key      string
	bucket   *rate.Limiter
	lastSeen time.Time
}

// Option adjusts a Limiter at construction.
type Option func(*Limiter)

// WithClientIPHeader makes the Limiter key requests on the named header
// instead of the connection's remote address. The Limiter trusts the header
// as is, so set it only when the collector is reachable solely through a proxy
// that writes it. For a comma-separated list such as X-Forwarded-For, the Limiter
// uses the last entry.
func WithClientIPHeader(name string) Option {
	return func(l *Limiter) { l.clientIPHeader = name }
}

// WithIPv6Prefix sets how many leading bits of an IPv6 address form its key.
// Every address with the same prefix shares one bucket. The default is 128.
// It ignores a value outside 1 to 128. IPv4 and IPv4-mapped addresses always
// key on the IPv4 address.
func WithIPv6Prefix(bits int) Option {
	return func(l *Limiter) {
		if bits >= 1 && bits <= 128 {
			l.ipv6Prefix = bits
		}
	}
}

func withStaleAfter(d time.Duration) Option {
	return func(l *Limiter) { l.staleAfter = d }
}

func withMaxVisitors(n int) Option {
	return func(l *Limiter) { l.maxVisitors = n }
}

// New returns a Limiter that allows r requests per second per client IP,
// with burst as the largest spike one client may send. It starts a goroutine
// that evicts idle clients. Call Stop when done.
func New(r rate.Limit, burst int, opts ...Option) *Limiter {
	l := &Limiter{
		rate:        r,
		burst:       burst,
		staleAfter:  defaultStaleAfter,
		maxVisitors: defaultMaxVisitors,
		ipv6Prefix:  DefaultIPv6Prefix,
		visitors:    make(map[string]*list.Element),
		order:       list.New(),
		stop:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	go l.evictStaleLoop()
	return l
}

// Stop ends the eviction goroutine. It is safe to call more than once.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

// Middleware wraps next. It rejects a request over the limit with 429 and a
// Retry-After header that gives the seconds until the client has a token.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retryAfter := l.allow(l.clientIP(r)); !ok {
			metrics.RateLimited.Inc()
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	now := time.Now()
	var v *visitor
	if el, found := l.visitors[key]; found {
		v = el.Value.(*visitor)
		l.order.MoveToFront(el)
	} else {
		for len(l.visitors) >= l.maxVisitors && l.order.Len() > 0 {
			oldest := l.order.Back()
			delete(l.visitors, oldest.Value.(*visitor).key)
			l.order.Remove(oldest)
		}
		v = &visitor{key: key, bucket: rate.NewLimiter(l.rate, l.burst)}
		l.visitors[key] = l.order.PushFront(v)
	}
	v.lastSeen = now
	bucket := v.bucket
	l.mu.Unlock()

	res := bucket.ReserveN(now, 1)
	if !res.OK() {
		return false, 0
	}
	delay := res.DelayFrom(now)
	if delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}

func retryAfterSeconds(d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return secs
}

func (l *Limiter) evictStaleLoop() {
	ticker := time.NewTicker(l.staleAfter)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			for el := l.order.Back(); el != nil; el = l.order.Back() {
				v := el.Value.(*visitor)
				if now.Sub(v.lastSeen) <= l.staleAfter {
					break
				}
				delete(l.visitors, v.key)
				l.order.Remove(el)
			}
			l.mu.Unlock()
		}
	}
}

func (l *Limiter) clientIP(r *http.Request) string {
	if l.clientIPHeader != "" {
		joined := strings.Join(r.Header.Values(l.clientIPHeader), ",")
		if ip := lastHeaderValue(joined); ip != "" {
			return l.normalizeIP(stripPort(ip))
		}
	}
	return l.normalizeIP(stripPort(r.RemoteAddr))
}

func (l *Limiter) normalizeIP(s string) string {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return textKey(s)
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(l.ipv6Prefix)
	if err != nil {
		return textKey(s)
	}
	return prefix.String()
}

func textKey(s string) string {
	if len(s) <= maxTextKey {
		return strings.Clone(s)
	}
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func lastHeaderValue(v string) string {
	parts := strings.Split(v, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(parts[i]); p != "" {
			return p
		}
	}
	return ""
}

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
