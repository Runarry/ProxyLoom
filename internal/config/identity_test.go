package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSetupCredentialFileAndProxyBoundary(t *testing.T) {
	env := apiEnvironment(t)
	env["PROXYLOOM_SETUP_TOKEN_FILE"] = secretFile(t, []byte(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))+"\n"))
	env["PROXYLOOM_TRUSTED_PROXIES"] = "127.0.0.1/32, ::1/128"
	cfg, err := LoadAPI(lookup(env))
	if err != nil {
		t.Fatal("valid identity configuration rejected")
	}
	token, err := cfg.ReadSetupToken()
	if err != nil || len(token) != 43 || len(cfg.TrustedProxies()) != 2 {
		t.Fatal("identity configuration not loaded")
	}
	clear(token)
	for _, malformed := range []string{"", "not-a-token", strings.Repeat("_", 43), strings.Repeat("A", 44), strings.Repeat("A", 43) + "\n\n"} {
		env["PROXYLOOM_SETUP_TOKEN_FILE"] = secretFile(t, []byte(malformed))
		if _, err := LoadAPI(lookup(env)); err == nil || strings.Contains(err.Error(), "not-a-token") {
			t.Fatal("invalid setup credential accepted or leaked")
		}
	}
	delete(env, "PROXYLOOM_SETUP_TOKEN_FILE")
	for _, invalid := range []string{"*", "localhost", "127.0.0.1", "127.0.0.1/32,", "127.0.0.1/99"} {
		env["PROXYLOOM_TRUSTED_PROXIES"] = invalid
		if _, err := LoadAPI(lookup(env)); err == nil {
			t.Fatal("invalid proxy configuration accepted")
		}
	}
}
