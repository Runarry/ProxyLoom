package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/netip"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$proxyloom$v=1$argon2id$v=19$m=65536,t=3,p=1$"

// The process-wide budget also covers separate Identity instances. Waiting
// requests hold no additional Argon memory and remain context-cancellable.
var passwordSlots = make(chan struct{}, 2)

func ValidPassword(password string) bool {
	return utf8.ValidString(password) && len(password) >= 12 && len(password) <= 1024 && !strings.ContainsAny(password, "\x00\r\n")
}

func ValidUsername(username string) bool {
	if len(username) < 1 || len(username) > 64 {
		return false
	}
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func ValidSource(source Source) bool {
	ip, err := netip.ParseAddr(source.IP)
	if err != nil || ip.Zone() != "" || ip.Unmap().String() != source.IP || len(source.RequestID) > 128 {
		return false
	}
	for _, c := range source.RequestID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func derivePassword(ctx context.Context, password string, salt []byte) ([]byte, error) {
	select {
	case passwordSlots <- struct{}{}:
		defer func() { <-passwordSlots }()
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	if ctx.Err() != nil {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func HashPassword(ctx context.Context, password string) (string, error) {
	if !ValidPassword(password) {
		return "", ErrInvalidInput
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", ErrUnavailable
	}
	key, err := derivePassword(ctx, password, salt)
	if err != nil {
		return "", err
	}
	defer clear(key)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// VerifyPassword accepts only the supported, bounded cost version. Tampered or
// future cost strings cannot allocate unbounded memory. Unknown users use the
// same KDF work through an invalid encoded hash to resist account timing probes.
func VerifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	if !utf8.ValidString(password) || len(password) > 1024 {
		return false, ErrInvalidInput
	}
	salt, expected := make([]byte, 16), make([]byte, 32)
	valid := false
	if strings.HasPrefix(encoded, passwordPrefix) {
		parts := strings.Split(strings.TrimPrefix(encoded, passwordPrefix), "$")
		if len(parts) == 2 {
			s, e1 := base64.RawStdEncoding.Strict().DecodeString(parts[0])
			h, e2 := base64.RawStdEncoding.Strict().DecodeString(parts[1])
			if e1 == nil && e2 == nil && len(s) == 16 && len(h) == 32 {
				copy(salt, s)
				copy(expected, h)
				valid = true
			}
			clear(h)
		}
	}
	actual, err := derivePassword(ctx, password, salt)
	if err != nil {
		return false, err
	}
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1 && valid, nil
}

func TokenBytes(value string) ([]byte, bool) {
	if len(value) != 43 {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return decoded, err == nil && len(decoded) == 32
}

func NewSessionToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func SessionDigest(pepper []byte, token string) ([]byte, bool) {
	bytes, ok := TokenBytes(token)
	if !ok || len(pepper) != 32 {
		return nil, false
	}
	defer clear(bytes)
	h := hmac.New(sha256.New, pepper)
	h.Write([]byte("proxyloom/admin/session/v1\x00"))
	h.Write(bytes)
	return h.Sum(nil), true
}

func CSRFToken(pepper []byte, token string) string {
	bytes, ok := TokenBytes(token)
	if !ok || len(pepper) != 32 {
		return ""
	}
	defer clear(bytes)
	h := hmac.New(sha256.New, pepper)
	h.Write([]byte("proxyloom/admin/csrf/v1\x00"))
	h.Write(bytes)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// DigestKey keeps untrusted account names out of rate/audit rows. Keys are
// separated by purpose, so a session digest cannot be used as another token.
func DigestKey(pepper []byte, purpose, value string) []byte {
	h := hmac.New(sha256.New, pepper)
	h.Write([]byte("proxyloom/admin/" + purpose + "/v1\x00"))
	h.Write([]byte(value))
	return h.Sum(nil)
}
