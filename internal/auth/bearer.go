// Package auth provides shared-secret authentication for the collector's
// agent-facing HTTP endpoints.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

const bearerPrefix = "Bearer "

// TokenSet holds the bearer tokens the ingest routes accept, as SHA-256
// hashes. The zero value accepts no token. It is safe for concurrent use.
type TokenSet struct {
	hashes atomic.Pointer[[][sha256.Size]byte]
}

// NewTokenSet returns a TokenSet that accepts each of tokens.
func NewTokenSet(tokens []string) *TokenSet {
	s := new(TokenSet)
	s.Store(tokens)
	return s
}

// Store replaces the accepted tokens with tokens. A concurrent Matches sees
// the old set or the new one, never a mix.
func (s *TokenSet) Store(tokens []string) {
	hashes := make([][sha256.Size]byte, len(tokens))
	for i, token := range tokens {
		hashes[i] = sha256.Sum256([]byte(token))
	}
	s.hashes.Store(&hashes)
}

// Matches reports whether presented equals any accepted token. The time it
// takes does not show an accepted token's length or which token matched.
func (s *TokenSet) Matches(presented string) bool {
	hashes := s.hashes.Load()
	if hashes == nil {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	match := 0
	for _, want := range *hashes {
		match |= subtle.ConstantTimeCompare(got[:], want[:])
	}
	return match == 1
}

// RequireBearerToken wraps next. It rejects with 401 a request whose
// "Authorization: Bearer <token>" header is missing or holds a token that is
// not in tokens. It matches the scheme name without regard to case. A Store
// on tokens applies from the next request.
func RequireBearerToken(tokens *TokenSet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !tokens.Matches(presented) {
			metrics.AuthRejected.Inc()
			w.Header().Set("WWW-Authenticate", `Bearer realm="otherlode-collector"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) (string, bool) {
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	return header[len(bearerPrefix):], true
}
