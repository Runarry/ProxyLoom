// Package config loads process-specific settings without exposing secret values in errors.
package config

import (
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Lookup func(string) string

type API struct {
	HTTPAddr, WebDir, PublicURL                                         string
	Development                                                         bool
	DatabaseDSNFile, MasterKeyFile, TokenPepperFile, ContentHMACKeyFile string
	MasterKeyID, OldMasterKeysFile                                      string
	SetupTokenFile, TrustedProxyCIDRs                                   string
}

type Migration struct{ DSNFile string }
type Runner struct{ HTTPAddr string }

func configError(name, reason string) error { return errors.New(name + ": " + reason) }

func LoadAPI(lookup Lookup) (API, error) {
	var c API
	var err error
	if c.HTTPAddr, err = HTTPAddress(lookup, ":8080"); err != nil {
		return API{}, err
	}
	c.WebDir = lookup("PROXYLOOM_WEB_DIR")
	if c.WebDir == "" {
		c.WebDir = "/app/web"
	}
	switch lookup("PROXYLOOM_DEV_MODE") {
	case "", "false":
	case "true":
		c.Development = true
	default:
		return API{}, configError("PROXYLOOM_DEV_MODE", "invalid_boolean")
	}
	c.PublicURL = lookup("PROXYLOOM_PUBLIC_URL")
	if !validPublicURL(c.PublicURL, c.Development) {
		return API{}, configError("PROXYLOOM_PUBLIC_URL", "invalid_url")
	}
	c.DatabaseDSNFile = lookup("PROXYLOOM_DATABASE_DSN_FILE")
	c.MasterKeyFile = lookup("PROXYLOOM_MASTER_KEY_FILE")
	c.MasterKeyID = lookup("PROXYLOOM_MASTER_KEY_ID")
	c.OldMasterKeysFile = lookup("PROXYLOOM_OLD_MASTER_KEYS_FILE")
	c.TokenPepperFile = lookup("PROXYLOOM_TOKEN_PEPPER_FILE")
	c.ContentHMACKeyFile = lookup("PROXYLOOM_CONTENT_HMAC_KEY_FILE")
	c.SetupTokenFile = lookup("PROXYLOOM_SETUP_TOKEN_FILE")
	c.TrustedProxyCIDRs = lookup("PROXYLOOM_TRUSTED_PROXIES")
	for _, cidr := range c.TrustedProxies() {
		if prefix, err := netip.ParsePrefix(cidr); err != nil || prefix.Addr().Is4In6() {
			return API{}, configError("PROXYLOOM_TRUSTED_PROXIES", "invalid_cidr")
		}
	}
	setupToken, err := c.ReadSetupToken()
	clear(setupToken)
	if err != nil {
		return API{}, err
	}
	if err = c.ValidateSecrets(); err != nil {
		return API{}, err
	}
	return c, nil
}

// Empty setup configuration disables initialization; an initialized deployment
// may remove its one-time file without disabling existing administrator access.
func (c API) ReadSetupToken() ([]byte, error) {
	if c.SetupTokenFile == "" {
		return nil, nil
	}
	data, err := readFile(c.SetupTokenFile, "PROXYLOOM_SETUP_TOKEN_FILE", 128)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	defer clear(decoded)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		return nil, configError("PROXYLOOM_SETUP_TOKEN_FILE", "invalid_token")
	}
	return []byte(token), nil
}

func (c API) TrustedProxies() []string {
	if c.TrustedProxyCIDRs == "" {
		return nil
	}
	values := strings.Split(c.TrustedProxyCIDRs, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values
}

func HTTPAddress(lookup Lookup, defaultAddr string) (string, error) {
	addr := lookup("PROXYLOOM_HTTP_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !validPort(port) || (host != "" && host != "localhost" && net.ParseIP(host) == nil) {
		return "", configError("PROXYLOOM_HTTP_ADDR", "invalid_listen_address")
	}
	return addr, nil
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validPublicURL(raw string, development bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return false
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !validPort(u.Port())) {
		return false
	}
	ip := net.ParseIP(host)
	if strings.HasPrefix(u.Host, "[") != strings.Contains(host, ":") || (strings.HasPrefix(u.Host, "[") && ip == nil) {
		return false
	}
	if ip == nil {
		if len(host) > 253 {
			return false
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return false
				}
			}
		}
	}
	return u.Scheme == "https" || (u.Scheme == "http" && development && (strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()))
}

func LoadMigration(lookup Lookup) (Migration, error) {
	c := Migration{DSNFile: lookup("PROXYLOOM_MIGRATION_DSN_FILE")}
	if _, err := c.ReadDSN(); err != nil {
		return Migration{}, err
	}
	return c, nil
}

func (c Migration) ReadDSN() (string, error) {
	return readDSN(c.DSNFile, "PROXYLOOM_MIGRATION_DSN_FILE")
}
func (c API) ReadDatabaseDSN() (string, error) {
	return readDSN(c.DatabaseDSNFile, "PROXYLOOM_DATABASE_DSN_FILE")
}

func readDSN(path, name string) (string, error) {
	data, err := readFile(path, name, 65536)
	if err != nil {
		return "", err
	}
	defer clear(data)
	dsn := strings.TrimSpace(string(data))
	if dsn == "" {
		return "", configError(name, "empty_secret")
	}
	return dsn, nil
}

func (c API) ValidateSecrets() error {
	if _, err := c.ReadDatabaseDSN(); err != nil {
		return err
	}
	keys, err := c.ReadKeys()
	if err != nil {
		return err
	}
	keys.Clear()
	return nil
}

func readFile(path, name string, limit int64) ([]byte, error) {
	if path == "" {
		return nil, configError(name, "required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, configError(name, "unreadable_file")
	}
	if !info.Mode().IsRegular() {
		return nil, configError(name, "not_regular_file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, configError(name, "unreadable_file")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, configError(name, "not_regular_file")
	}
	if info.Size() > limit {
		return nil, configError(name, "secret_too_large")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		clear(data)
		return nil, configError(name, "unreadable_file")
	}
	if int64(len(data)) > limit {
		clear(data)
		return nil, configError(name, "secret_too_large")
	}
	return data, nil
}

func LoadRunner(environ []string) (Runner, error) {
	addr := ""
	seen := false
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "PROXYLOOM_") {
			continue
		}
		if name != "PROXYLOOM_HTTP_ADDR" {
			return Runner{}, configError("PROXYLOOM_*", "runner_environment_forbidden")
		}
		if seen {
			return Runner{}, configError("PROXYLOOM_HTTP_ADDR", "duplicate_variable")
		}
		seen, addr = true, value
	}
	listen, err := HTTPAddress(func(string) string { return addr }, "127.0.0.1:9092")
	if err != nil {
		return Runner{}, err
	}
	host, port, _ := net.SplitHostPort(listen)
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || port != "9092" {
		return Runner{}, configError("PROXYLOOM_HTTP_ADDR", "runner_requires_loopback_9092")
	}
	return Runner{HTTPAddr: listen}, nil
}
