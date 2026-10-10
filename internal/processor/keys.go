package processor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"unicode/utf16"
)

// MinSecretBytes is the shortest redaction secret the collector accepts.
// 32 bytes is the size of an HMAC-SHA256 key that adds no weakness of its
// own. `openssl rand -hex 32` prints a secret of 64 bytes.
const MinSecretBytes = 32

// keyBytes is how many bytes of the HMAC a re-keyed key keeps. The agent's
// keys are the first 16 bytes of a SHA-256 digest, so a re-keyed key has
// the same 32 lowercase hex characters.
const keyBytes = 16

// fingerprintLabel is the message whose HMAC under the secret gives the
// secret's fingerprint. No agent key has this form, so a fingerprint never
// equals a re-keyed key.
const fingerprintLabel = "otherlode-collector redaction secret fingerprint v1"

// fingerprintBytes is how many bytes of that HMAC the fingerprint keeps.
const fingerprintBytes = 8

// CheckSecret returns an error when secret is too short to key the HMAC
// or holds a control character. The error never quotes the secret.
func CheckSecret(secret []byte) error {
	if len(secret) < MinSecretBytes {
		return fmt.Errorf("the secret is %d bytes, want at least %d", len(secret), MinSecretBytes)
	}
	for i, c := range secret {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("the secret holds a control character at byte %d", i)
		}
	}
	return nil
}

// SecretFingerprint returns 16 lowercase hex characters that name secret
// without revealing it: the first 8 bytes of HMAC-SHA256(secret,
// fingerprintLabel). Two collectors with the same secret log the same
// fingerprint, so an operator can compare them.
func SecretFingerprint(secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	io.WriteString(mac, fingerprintLabel)
	return hex.EncodeToString(mac.Sum(nil)[:fingerprintBytes])
}

// rekeyer replaces an agent key with HMAC-SHA256(secret, key), cut to
// keyBytes and written as lowercase hex. It is not safe for concurrent
// use, so each payload gets its own.
type rekeyer struct {
	mac hash.Hash
	sum []byte
}

func newRekeyer(secret []byte) *rekeyer {
	return &rekeyer{mac: hmac.New(sha256.New, secret)}
}

func (k *rekeyer) rekey(key string) string {
	k.mac.Reset()
	io.WriteString(k.mac, key)
	k.sum = k.mac.Sum(k.sum[:0])
	return hex.EncodeToString(k.sum[:keyBytes])
}

// javaStringHash returns what Java's String.hashCode() returns for s. Java
// hashes UTF-16 code units, and the int arithmetic wraps on overflow, as
// Go's int32 arithmetic does.
func javaStringHash(s string) int32 {
	var h int32
	for _, unit := range utf16.Encode([]rune(s)) {
		h = 31*h + int32(unit)
	}
	return h
}

var errSecretWithoutRedaction = errors.New("a redaction secret is set but redaction is off")
