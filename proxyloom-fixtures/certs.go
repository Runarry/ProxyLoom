package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func runInitCerts(args []string) error {
	flags := flag.NewFlagSet("init-certs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	out := flags.String("out", ".cache/isolation/certs", "test-only certificate output directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *out == "" {
		return errors.New("usage: proxyloom-fixtures init-certs [--out directory]")
	}
	return initCerts(*out)
}

func initCerts(out string) error {
	if _, err := os.Lstat(out); err == nil {
		return errors.New("certificate output directory already exists; refusing overwrite")
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect certificate output directory")
	}
	ca, err := isolation.NewTestCA()
	if err != nil {
		return errors.New("cannot generate test CA")
	}
	files := map[string][]byte{"ca.pem": ca.PEM}
	for _, role := range []string{"a", "b"} {
		leaf, err := ca.Issue(role + ".proxyloom.test")
		if err != nil {
			return errors.New("cannot generate test leaf certificate")
		}
		key, err := x509.MarshalPKCS8PrivateKey(leaf.Certificate.PrivateKey)
		if err != nil {
			return errors.New("cannot encode test leaf key")
		}
		files[role+".pem"] = leaf.PEM
		files[role+"-key.pem"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	}
	// Only synthetic leaf keys are exported. The shared CA private key stays in memory.
	if err := makeReadableParents(filepath.Dir(out)); err != nil {
		return errors.New("cannot create certificate parent directories")
	}
	if err := os.Mkdir(out, 0755); err != nil {
		return errors.New("cannot create certificate output directory; existing paths are never overwritten")
	}
	if err := os.Chmod(out, 0755); err != nil {
		return errors.New("cannot set certificate directory permissions")
	}
	for _, name := range []string{"ca.pem", "a.pem", "a-key.pem", "b.pem", "b-key.pem"} {
		if err := writeTestCertificate(filepath.Join(out, name), files[name]); err != nil {
			return fmt.Errorf("cannot write %s; partial output retained, choose a new output directory", name)
		}
	}
	return nil
}

// Make only newly created directories traversable by the test container UID,
// including when the caller uses a restrictive umask; preserve existing parents.
func makeReadableParents(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return errors.New("parent is not a directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return errors.New("parent directory root does not exist")
	}
	if err := makeReadableParents(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0755); err != nil {
		return err
	}
	return os.Chmod(path, 0755)
}

func writeTestCertificate(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	// TEST ONLY: synthetic keys must be readable by container UID 10003.
	if err := file.Chmod(0644); err != nil {
		return err
	}
	return file.Close()
}
