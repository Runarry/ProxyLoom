// Package bootstrap creates private deployment files for the local operator.
// It never contacts a service or replaces an existing key or identity bundle.
package bootstrap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
)

var ErrBundle = errors.New("deployment_bundle_invalid_or_already_exists")

type Info struct {
	Version            int       `json:"schema_version"`
	Architecture       string    `json:"architecture"`
	RunnerID           ir.ID     `json:"runner_id"`
	AuthorizationEpoch string    `json:"authorization_epoch,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// Runner creates a fresh certificate pair and a registry for the locked image.
// The CA signing key is not persisted; renewal replaces the identity bundle.
func Runner(architecture, epoch string) (map[string][]byte, Info, error) {
	if architecture != "amd64" && architecture != "arm64" || epoch != "" && ir.ID(epoch).Validate() != nil {
		return nil, Info{}, ErrBundle
	}
	catalog, err := capability.Load()
	if err != nil {
		return nil, Info{}, ErrBundle
	}
	now := time.Now().UTC()
	info := Info{Version: 1, Architecture: architecture, RunnerID: jobs.NewID(), AuthorizationEpoch: epoch, CreatedAt: now}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, Info{}, ErrBundle
	}
	serial := func() *big.Int {
		v, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil
		}
		return v
	}
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "ProxyLoom runner control CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(2, 0, 1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	if ca.SerialNumber == nil {
		return nil, Info{}, ErrBundle
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, Info{}, ErrBundle
	}
	files := map[string][]byte{"runner_ca": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
	var fingerprint string
	for _, role := range []string{"server", "client"} {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, Info{}, ErrBundle
		}
		cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: string(info.RunnerID)}, NotBefore: ca.NotBefore, NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if cert.SerialNumber == nil {
			return nil, Info{}, ErrBundle
		}
		if role == "server" {
			cert.Subject.CommonName = "api"
			cert.DNSNames = []string{"api", "localhost"}
			cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if err != nil {
			return nil, Info{}, ErrBundle
		}
		encoded, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, Info{}, ErrBundle
		}
		files["runner_"+role+"_cert"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		files["runner_"+role+"_key"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
		clear(encoded)
		if role == "client" {
			digest := sha256.Sum256(der)
			fingerprint = hex.EncodeToString(digest[:])
		}
	}
	ids := []ir.ID{}
	for _, build := range catalog.Builds() {
		if build.OS == "linux" && build.Arch == architecture {
			ids = append(ids, build.ID)
		}
	}
	if len(ids) != 3 {
		return nil, Info{}, ErrBundle
	}
	files["runner_registry"], err = json.Marshal([]runnercontrol.Registration{{RunnerID: info.RunnerID, Architecture: architecture, AuthorizationEpoch: epoch, CertificateSHA256: fingerprint, CoreBuildIDs: ids, ValidationSlots: 1, ConnectivitySlots: 4, ThroughputSlots: 1}})
	if err != nil {
		return nil, Info{}, ErrBundle
	}
	files["runner.env"] = []byte("PROXYLOOM_RUNNER_ID=" + string(info.RunnerID) + "\n")
	return files, info, nil
}

func Create(directory, architecture, publicURL, epoch string, runnerOnly bool) (Info, error) {
	if !filepath.IsAbs(directory) {
		return Info{}, ErrBundle
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Info{}, ErrBundle
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		return Info{}, ErrBundle
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return Info{}, ErrBundle
	}
	files, info, err := Runner(architecture, epoch)
	if err != nil {
		return Info{}, err
	}
	defer func() {
		for _, data := range files {
			clear(data)
		}
	}()
	if !runnerOnly {
		u, err := url.Parse(publicURL)
		development := err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
		if err != nil || !config.ValidPublicURL(publicURL, development) || (u.Path != "" && u.Path != "/") {
			return Info{}, ErrBundle
		}
		random := func() ([]byte, error) { b := make([]byte, 32); _, err := rand.Read(b); return b, err }
		for _, name := range []string{"master_key", "token_pepper", "content_hmac_key", "setup_token", "metrics_token", "db_bootstrap_password", "db_runtime_password", "db_migration_password"} {
			data, err := random()
			if err != nil {
				return Info{}, ErrBundle
			}
			switch name {
			case "setup_token", "metrics_token":
				files[name] = []byte(base64.RawURLEncoding.EncodeToString(data))
				clear(data)
			case "db_bootstrap_password", "db_runtime_password", "db_migration_password":
				files[name] = []byte(hex.EncodeToString(data))
				clear(data)
			default:
				files[name] = data
			}
		}
		for _, item := range []struct{ name, user, password string }{{"database_dsn", "proxyloom", "db_runtime_password"}, {"migration_dsn", "proxyloom_migrator", "db_migration_password"}} {
			dsn := url.URL{Scheme: "postgres", User: url.UserPassword(item.user, string(files[item.password])), Host: "postgres:5432", Path: "/proxyloom", RawQuery: "sslmode=disable"}
			files[item.name] = []byte(dsn.String())
		}
		backupKey, err := age.GenerateX25519Identity()
		if err != nil {
			return Info{}, ErrBundle
		}
		files["backup_identity"] = []byte(backupKey.String() + "\n")
		files["backup_recipient"] = []byte(backupKey.Recipient().String() + "\n")
		files["old_master_keys"] = []byte("{}\n")
		files["master-key-id"] = []byte("master-v1\n")
		files["deployment.env"] = []byte(fmt.Sprintf("PROXYLOOM_PUBLIC_URL=%s\nPROXYLOOM_DEV_MODE=%t\nPROXYLOOM_RUNNER_ID=%s\nPROXYLOOM_MASTER_KEY_ID=master-v1\n", strings.TrimSuffix(publicURL, "/"), development, info.RunnerID))
	}
	created := []string{}
	complete := false
	defer func() {
		if !complete {
			for _, name := range created {
				_ = os.Remove(filepath.Join(directory, name))
			}
		}
	}()
	for name, data := range files {
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
		if err != nil {
			return Info{}, ErrBundle
		}
		created = append(created, name)
		_, writeErr := file.Write(data)
		modeErr := file.Chmod(0444)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || modeErr != nil || syncErr != nil || closeErr != nil {
			return Info{}, ErrBundle
		}
	}
	// Individual file mounts must be readable by the service UID. The host's
	// 0700 parent protects them; never mount the whole bundle into a service.
	manifest, _ := json.Marshal(info)
	if err = os.WriteFile(filepath.Join(directory, "bundle.json"), manifest, 0600); err != nil {
		return Info{}, ErrBundle
	}
	complete = true
	return info, nil
}
