// Package isolation provides rebuildable, test-only Trojan and HTTP fixtures.
// Production API and Runner binaries must not import this package.
package isolation

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

const TestOnlyLabel = "ProxyLoom TEST ONLY"

var certValidityStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type CertificateAuthority struct {
	Certificate *x509.Certificate
	PrivateKey  *rsa.PrivateKey
	PEM         []byte
	Pool        *x509.CertPool
}

type Leaf struct {
	Host        string
	Certificate tls.Certificate
	PEM         []byte
}

func NewTestCA() (*CertificateAuthority, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ProxyLoom Isolation Test CA", Organization: []string{TestOnlyLabel}},
		NotBefore:             certValidityStart,
		NotAfter:              certValidityStart.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	return &CertificateAuthority{Certificate: cert, PrivateKey: key, PEM: pemBytes, Pool: pool}, nil
}

func (ca *CertificateAuthority) Issue(host string) (Leaf, error) {
	if ca == nil || ca.Certificate == nil || ca.PrivateKey == nil {
		return Leaf{}, fmt.Errorf("test ca is required")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Leaf{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return Leaf{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{TestOnlyLabel}},
		DNSNames:     []string{host},
		NotBefore:    certValidityStart,
		NotAfter:     certValidityStart.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
		template.DNSNames = nil
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Certificate, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return Leaf{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Leaf{}, err
	}
	return Leaf{Host: host, Certificate: tlsCert, PEM: certPEM}, nil
}
