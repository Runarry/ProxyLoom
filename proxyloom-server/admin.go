package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/storage"
)

// Commands accept secret file paths, never password values on the command line.
// All returned errors are fixed safe codes; even filesystem paths are omitted.
func adminCommand(ctx context.Context, args []string, lookup config.Lookup, output io.Writer) error {
	if len(args) == 3 && args[0] == "create-setup-token" && args[1] == "--output" {
		return createSetupToken(args[2], output)
	}
	if len(args) != 5 || args[0] != "reset-password" || args[1] != "--username" || args[3] != "--password-file" {
		return errors.New("admin_invalid_command")
	}
	password, err := readAdminPassword(args[4])
	if err != nil {
		return err
	}
	defer clear(password)
	// The controlled host command needs runtime DB authority and token pepper,
	// not a public listener or setup credential. It cannot register new admins.
	cfg := config.API{DatabaseDSNFile: lookup("PROXYLOOM_DATABASE_DSN_FILE")}
	dsn, err := cfg.ReadDatabaseDSN()
	if err != nil {
		return errors.New("admin_database_configuration_invalid")
	}
	pepper, err := readAdminKey(lookup("PROXYLOOM_TOKEN_PEPPER_FILE"))
	if err != nil {
		return err
	}
	defer clear(pepper)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := storage.Open(ctx, dsn)
	if err != nil {
		return errors.New("admin_database_unavailable")
	}
	defer pool.Close()
	service, err := storage.NewIdentity(pool, identity.Options{ScopeID: identity.DefaultScopeID, TokenPepper: pepper})
	if err != nil {
		return errors.New("admin_identity_unavailable")
	}
	if err := service.ResetPassword(ctx, args[2], string(password)); err != nil {
		return errors.New("admin_password_reset_failed")
	}
	_, err = io.WriteString(output, "Administrator password reset; all prior administrator sessions revoked.\n")
	if err != nil {
		return errors.New("admin_output_failed")
	}
	return nil
}

func createSetupToken(path string, output io.Writer) error {
	if !filepath.IsAbs(path) {
		return errors.New("admin_output_path_must_be_absolute")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("admin_random_failed")
	}
	defer clear(random[:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("admin_setup_file_create_failed")
	}
	token := []byte(base64.RawURLEncoding.EncodeToString(random[:]))
	defer clear(token)
	_, writeErr := file.Write(token)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("admin_setup_file_write_failed")
	}
	if _, err := io.WriteString(output, "Created one-time setup credential in the specified file. Existing files are never replaced.\n"); err != nil {
		return errors.New("admin_output_failed")
	}
	return nil
}

func readAdminFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("admin_secret_file_invalid")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("admin_secret_file_invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, errors.New("admin_secret_file_invalid")
	}
	return data, nil
}

func readAdminPassword(path string) ([]byte, error) {
	data, err := readAdminFile(path, 1026)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(password) < 12 || len(password) > 1024 || !utf8.ValidString(password) || strings.ContainsAny(password, "\x00\r\n") {
		return nil, errors.New("admin_password_invalid")
	}
	return []byte(password), nil
}

func readAdminKey(path string) ([]byte, error) {
	data, err := readAdminFile(path, 32)
	if err != nil || len(data) != 32 {
		clear(data)
		return nil, errors.New("admin_token_pepper_invalid")
	}
	return data, nil
}
