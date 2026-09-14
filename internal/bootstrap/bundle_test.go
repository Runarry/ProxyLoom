package bootstrap

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
)

func TestRunnerBundleBindsCertificatesEpochAndArchitecture(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		epoch := string(jobs.NewID())
		files, info, err := Runner(arch, epoch)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			for _, data := range files {
				clear(data)
			}
		}()
		if _, exists := files["runner_ca_key"]; exists {
			t.Fatal("CA private key was persisted")
		}
		root := x509.NewCertPool()
		if !root.AppendCertsFromPEM(files["runner_ca"]) {
			t.Fatal("invalid CA")
		}
		var registry []runnercontrol.Registration
		if json.Unmarshal(files["runner_registry"], &registry) != nil || len(registry) != 1 {
			t.Fatal("invalid registry")
		}
		r := registry[0]
		if r.AuthorizationEpoch != epoch || r.Architecture != arch || r.RunnerID != info.RunnerID || len(r.CoreBuildIDs) != 3 || r.ConnectivitySlots != 4 || r.ThroughputSlots != 1 {
			t.Fatal("registry identity or capacity mismatch")
		}
		for _, role := range []string{"server", "client"} {
			pair, err := tls.X509KeyPair(files["runner_"+role+"_cert"], files["runner_"+role+"_key"])
			if err != nil {
				t.Fatal("certificate key mismatch")
			}
			cert, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			opts := x509.VerifyOptions{Roots: root, DNSName: "api", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			if role == "client" {
				opts.DNSName = ""
				opts.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
				hash := sha256.Sum256(cert.Raw)
				if r.CertificateSHA256 != hex.EncodeToString(hash[:]) {
					t.Fatal("client fingerprint mismatch")
				}
			}
			if _, err = cert.Verify(opts); err != nil {
				t.Fatal("certificate trust failed")
			}
		}
	}
}
