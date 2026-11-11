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
const MinSecretBytes = 32

// A re-keyed key must have the same form as an agent key: 32 lowercase hex characters.
const keyBytes = 16

const fingerprintLabel = "otherlode-collector redaction secret fingerprint v1"

const fingerprintBytes = 8

// CheckSecret returns an error when secret is shorter than MinSecretBytes or
// holds a control character. The error never quotes the secret.
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
// without revealing it. Collectors with the same secret return the same
// fingerprint.
func SecretFingerprint(secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	io.WriteString(mac, fingerprintLabel)
	return hex.EncodeToString(mac.Sum(nil)[:fingerprintBytes])
}

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

func javaStringHash(s string) int32 {
	var h int32
	for _, unit := range utf16.Encode([]rune(s)) {
		h = 31*h + int32(unit)
	}
	return h
}

var errSecretWithoutRedaction = errors.New("a redaction secret is set but redaction is off")
