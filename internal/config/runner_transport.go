package config

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// RunnerTransport contains only this runner's identity, control-plane trust and
// immutable binary directory. It deliberately has no API/database key fields.
type RunnerTransport struct {
	HTTPAddr, APIURL, RunnerID, CoreRoot string
	CAFile, CertificateFile, KeyFile     string
}

// RunnerListener is optional on the API process. If configured, all TLS and
// registration files are required and the listener is separate from public HTTP.
type RunnerListener struct {
	Address, CAFile, CertificateFile, KeyFile, RegistryFile string
}

func LoadRunnerTransport(environ []string) (RunnerTransport, error) {
	allowed := map[string]bool{
		"PROXYLOOM_HTTP_ADDR": true, "PROXYLOOM_RUNNER_API_URL": true,
		"PROXYLOOM_RUNNER_ID": true, "PROXYLOOM_RUNNER_CORE_ROOT": true,
		"PROXYLOOM_RUNNER_CA_FILE": true, "PROXYLOOM_RUNNER_CERT_FILE": true,
		"PROXYLOOM_RUNNER_KEY_FILE": true,
	}
	values := make(map[string]string)
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "PROXYLOOM_") {
			continue
		}
		if !allowed[name] {
			return RunnerTransport{}, configError("PROXYLOOM_*", "runner_environment_forbidden")
		}
		if _, exists := values[name]; exists {
			return RunnerTransport{}, configError(name, "duplicate_variable")
		}
		values[name] = value
	}
	healthEnv := []string{}
	if address, exists := values["PROXYLOOM_HTTP_ADDR"]; exists {
		healthEnv = append(healthEnv, "PROXYLOOM_HTTP_ADDR="+address)
	}
	health, err := LoadRunner(healthEnv)
	if err != nil {
		return RunnerTransport{}, err
	}
	c := RunnerTransport{
		HTTPAddr: health.HTTPAddr, APIURL: values["PROXYLOOM_RUNNER_API_URL"],
		RunnerID: values["PROXYLOOM_RUNNER_ID"], CoreRoot: values["PROXYLOOM_RUNNER_CORE_ROOT"],
		CAFile: values["PROXYLOOM_RUNNER_CA_FILE"], CertificateFile: values["PROXYLOOM_RUNNER_CERT_FILE"],
		KeyFile: values["PROXYLOOM_RUNNER_KEY_FILE"],
	}
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.Contains(c.APIURL, "#") || (u.Port() != "" && !validPort(u.Port())) || strings.HasSuffix(u.Host, ":") {
		return RunnerTransport{}, configError("PROXYLOOM_RUNNER_API_URL", "invalid_https_origin")
	}
	if !runnerUUID(c.RunnerID) {
		return RunnerTransport{}, configError("PROXYLOOM_RUNNER_ID", "invalid_uuid")
	}
	for name, path := range map[string]string{
		"PROXYLOOM_RUNNER_CORE_ROOT": c.CoreRoot, "PROXYLOOM_RUNNER_CA_FILE": c.CAFile,
		"PROXYLOOM_RUNNER_CERT_FILE": c.CertificateFile, "PROXYLOOM_RUNNER_KEY_FILE": c.KeyFile,
	} {
		if !filepath.IsAbs(path) || !validKeyPath(path) {
			return RunnerTransport{}, configError(name, "absolute_path_required")
		}
	}
	return c, nil
}

func LoadRunnerListener(lookup Lookup) (RunnerListener, error) {
	c := RunnerListener{
		Address: lookup("PROXYLOOM_RUNNER_LISTEN_ADDR"), CAFile: lookup("PROXYLOOM_RUNNER_CA_FILE"),
		CertificateFile: lookup("PROXYLOOM_RUNNER_CERT_FILE"), KeyFile: lookup("PROXYLOOM_RUNNER_KEY_FILE"),
		RegistryFile: lookup("PROXYLOOM_RUNNER_REGISTRY_FILE"),
	}
	if c == (RunnerListener{}) {
		return c, nil
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || !validPort(port) || (host != "" && net.ParseIP(host) == nil) {
		return RunnerListener{}, configError("PROXYLOOM_RUNNER_LISTEN_ADDR", "invalid_listen_address")
	}
	for name, path := range map[string]string{
		"PROXYLOOM_RUNNER_CA_FILE": c.CAFile, "PROXYLOOM_RUNNER_CERT_FILE": c.CertificateFile,
		"PROXYLOOM_RUNNER_KEY_FILE": c.KeyFile, "PROXYLOOM_RUNNER_REGISTRY_FILE": c.RegistryFile,
	} {
		if !validKeyPath(path) {
			return RunnerListener{}, configError(name, "absolute_path_required")
		}
	}
	return c, nil
}

func (c RunnerTransport) TLS() (*tls.Config, error) {
	conf, err := runnerTLS(c.CAFile, c.CertificateFile, c.KeyFile)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, configError("PROXYLOOM_RUNNER_API_URL", "invalid_https_origin")
	}
	conf.ServerName = u.Hostname()
	return conf, nil
}

func (c RunnerListener) TLS() (*tls.Config, error) {
	conf, err := runnerTLS(c.CAFile, c.CertificateFile, c.KeyFile)
	if err != nil {
		return nil, err
	}
	conf.ClientCAs = conf.RootCAs
	conf.RootCAs = nil
	conf.ClientAuth = tls.RequireAndVerifyClientCert
	return conf, nil
}

func runnerTLS(caFile, certFile, keyFile string) (*tls.Config, error) {
	ca, err := readFile(caFile, "PROXYLOOM_RUNNER_CA_FILE", 64<<10)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, configError("PROXYLOOM_RUNNER_CA_FILE", "invalid_ca")
	}
	cert, err := readFile(certFile, "PROXYLOOM_RUNNER_CERT_FILE", 64<<10)
	if err != nil {
		return nil, err
	}
	key, err := readFile(keyFile, "PROXYLOOM_RUNNER_KEY_FILE", 64<<10)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, configError("PROXYLOOM_RUNNER_CERT_FILE", "invalid_certificate_pair")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: pool}, nil
}

func runnerUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != "00000000-0000-0000-0000-000000000000"
}
