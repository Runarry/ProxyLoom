package main

import (
	"crypto/tls"
	"errors"
	"net"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func loadProxyConfig(role string, lookup func(string) (string, bool)) (isolation.ProxyConfig, error) {
	cfg := isolation.ProxyConfig{Role: role, Log: &isolation.Log{}}
	if role != "a" && role != "b" {
		return cfg, errors.New("proxy role must be a or b")
	}
	get := func(name string) string { value, _ := lookup("PROXYLOOM_ISOLATION_" + name); return value }
	cfg.Bind, cfg.Host, cfg.Password = get("BIND"), get("HOST"), get("PASSWORD")
	if cfg.Host == "" {
		cfg.Host = role + ".proxyloom.test"
	}
	if cfg.Password == "" {
		cfg.Password = "EXAMPLE_ONLY_" + strings.ToUpper(role)
	}
	allow, present := lookup("PROXYLOOM_ISOLATION_ALLOW_FROM")
	if role == "b" && !present {
		return cfg, errors.New("role b requires nonempty PROXYLOOM_ISOLATION_ALLOW_FROM")
	}
	if present {
		for _, part := range strings.Split(allow, ",") {
			ip := net.ParseIP(strings.TrimSpace(part))
			if ip == nil {
				return cfg, errors.New("PROXYLOOM_ISOLATION_ALLOW_FROM must contain only nonempty IP addresses")
			}
			cfg.AllowFrom = append(cfg.AllowFrom, ip)
		}
	}
	certFile, keyFile := get("CERT_FILE"), get("KEY_FILE")
	if certFile == "" || keyFile == "" {
		return cfg, errors.New("PROXYLOOM_ISOLATION_CERT_FILE and PROXYLOOM_ISOLATION_KEY_FILE are required; run init-certs first")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// Do not include user-supplied paths or certificate/key contents in logs.
		return cfg, errors.New("cannot load isolation certificate/key pair: files must be readable, valid PEM, and matching")
	}
	cfg.Certificate = cert
	return cfg, nil
}
