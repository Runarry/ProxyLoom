package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestPasswordArgonVersionSaltAndBoundedVerification(t *testing.T) {
	ctx := context.Background()
	password := "synthetic correct password 7!"
	one, err := HashPassword(ctx, password)
	if err != nil {
		t.Fatal("hash failed")
	}
	two, err := HashPassword(ctx, password)
	if err != nil || one == two || !strings.HasPrefix(one, passwordPrefix) || strings.Contains(one, password) {
		t.Fatal("hash encoding or random salt invalid")
	}
	for _, test := range []struct {
		password, hash string
		want           bool
	}{
		{password, one, true}, {"incorrect synthetic password", one, false}, {password, "", false},
		{password, strings.Replace(one, "m=65536", "m=4294967295", 1), false},
		{password, strings.Replace(one, "$v=1$", "$v=2$", 1), false},
		{password, one + "$unexpected", false},
	} {
		got, err := VerifyPassword(ctx, test.password, test.hash)
		if err != nil || got != test.want {
			t.Fatal("password verification failed its bounded positive/negative case")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := HashPassword(cancelled, password); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled hashing was not stopped")
	}
}

func TestIdentityTokenSeparationCanonicalEncodingAndRedaction(t *testing.T) {
	pepper := bytes.Repeat([]byte{0x37}, 32)
	one, err := NewSessionToken()
	if err != nil {
		t.Fatal("could not create synthetic session")
	}
	two, err := NewSessionToken()
	if err != nil || one == two || len(one) != 43 {
		t.Fatal("invalid random session")
	}
	digest, ok := SessionDigest(pepper, one)
	csrf := CSRFToken(pepper, one)
	if !ok || len(digest) != 32 || csrf == one || len(csrf) != 43 || bytes.Equal(digest, DigestKey(pepper, "audit/source", one)) {
		t.Fatal("purpose separation failed")
	}
	if csrf != CSRFToken(pepper, one) || csrf == CSRFToken(bytes.Repeat([]byte{0x38}, 32), one) {
		t.Fatal("CSRF persistence/key binding failed")
	}
	for _, malformed := range []string{"", one + "=", one[:42], "sub_" + one, base64.StdEncoding.EncodeToString(digest)} {
		if _, ok := SessionDigest(pepper, malformed); ok {
			t.Fatal("noncanonical or foreign credential accepted")
		}
	}
	// Raw URL base64 has unused low bits in its final character. Strict decode
	// rejects alternate text encodings of the same 32 bytes.
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, one[42])
	if _, ok := TokenBytes(one[:42] + string(alphabet[index+1])); ok {
		t.Fatal("noncanonical trailing bits accepted")
	}
	session := Session{ID: one, CSRFToken: csrf}
	options := Options{SetupToken: []byte(one), TokenPepper: pepper}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("synthetic", "session", session, "options", options)
	formatted := fmt.Sprintf("%+v %#v", session, options) + logged.String()
	if strings.Contains(formatted, one) || strings.Contains(formatted, csrf) {
		t.Fatal("credential aggregate leaked into logs")
	}
	encoded, err := json.Marshal(session)
	if err != nil || bytes.Contains(encoded, []byte(one)) || !bytes.Contains(encoded, []byte(csrf)) {
		t.Fatal("session response serialization invalid")
	}
}

func TestIdentityInputBounds(t *testing.T) {
	for _, name := range []string{"", "Admin", "admin ", "管理员", strings.Repeat("a", 65), "a\n"} {
		if ValidUsername(name) {
			t.Fatal("ambiguous username accepted")
		}
	}
	if !ValidUsername("admin_1.demo-test") || !ValidPassword(strings.Repeat("a", 12)) || !ValidPassword(strings.Repeat("a", 1024)) ||
		ValidPassword(strings.Repeat("a", 11)) || ValidPassword(strings.Repeat("a", 1025)) || ValidPassword(string([]byte{0xff})) {
		t.Fatal("input bounds invalid")
	}
	for _, source := range []Source{{IP: "127.0.0.1:443"}, {IP: "::ffff:127.0.0.1"}, {IP: "fe80::1%ethernet"}, {IP: "127.0.0.1", RequestID: "bad\nheader"}} {
		if ValidSource(source) {
			t.Fatal("untrusted audit source accepted")
		}
	}
	if !ValidSource(Source{IP: "127.0.0.1", RequestID: "r:1-2.3_4"}) || !ValidSource(Source{IP: "2001:db8::1"}) {
		t.Fatal("canonical source rejected")
	}
}
