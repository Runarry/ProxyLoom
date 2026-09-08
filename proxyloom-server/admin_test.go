package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminSetupFileExclusiveAndNoSecretOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup")
	var output bytes.Buffer
	noEnv := func(string) string { t.Fatal("setup generation read service credentials"); return "" }
	if err := adminCommand(context.Background(), []string{"create-setup-token", "--output", path}, noEnv, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(string(data))
	if err != nil || len(decoded) != 32 || output.Len() == 0 || strings.Contains(output.String(), string(data)) {
		t.Fatal("setup generation did not protect credential")
	}
	if err := createSetupToken(path, io.Discard); err == nil {
		t.Fatal("setup file overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("existing setup credential changed")
	}
	if err := createSetupToken("relative-path", io.Discard); err == nil {
		t.Fatal("relative output path accepted")
	}
}

func TestAdminPasswordFilesKeepWhitespaceAndRejectOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	password := "  EXAMPLE_ADMIN_PASSWORD  "
	if err := os.WriteFile(path, []byte(password+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readAdminPassword(path)
	if err != nil || string(got) != password {
		t.Fatal("password file normalization changed password")
	}
	for _, bad := range []string{"short", strings.Repeat("x", 1025), "EXAMPLE_PASSWORD\nSECOND_LINE", "EXAMPLE_PASSWORD\x00"} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAdminPassword(path); err == nil {
			t.Fatal("invalid password file accepted")
		}
	}
	if err := adminCommand(context.Background(), []string{"reset-password", "--password", "EXAMPLE_SECRET"}, func(string) string { return "" }, io.Discard); err == nil || strings.Contains(err.Error(), "EXAMPLE_SECRET") {
		t.Fatal("unsafe command boundary")
	}
}
