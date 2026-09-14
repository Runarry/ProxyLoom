package backup

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"filippo.io/age"
	"github.com/Runarry/ProxyLoom/internal/config"
)

func testKeys() config.KeyMaterial {
	return config.KeyMaterial{ActiveKeyID: "master-1", MasterKeys: map[string][]byte{"master-1": bytes.Repeat([]byte{1}, 32), "master-old": bytes.Repeat([]byte{2}, 32)}, TokenPepper: bytes.Repeat([]byte{3}, 32), ContentHMACKey: bytes.Repeat([]byte{4}, 32)}
}
func TestArchiveAuthenticatesFullStreamAndAllRequiredKeys(t *testing.T) {
	i, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keys := testKeys()
	defer keys.Clear()
	dump := append([]byte("PGDMP"), bytes.Repeat([]byte("synthetic database fixture"), 20000)...)
	var out bytes.Buffer
	w, m, err := Encrypt(&out, i.Recipient().String(), keys, 21)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(dump); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), dump[:30]) || bytes.Contains(out.Bytes(), []byte(m.KeyCheck)) {
		t.Fatal("plaintext archive metadata escaped encryption")
	}
	verified, err := Verify(bytes.NewReader(out.Bytes()), i.String(), keys)
	if err != nil || verified.BackupID != m.BackupID {
		t.Fatal("valid archive rejected", err)
	}
	r, _, err := Decrypt(bytes.NewReader(out.Bytes()), i.String(), keys)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(plain, dump) {
		t.Fatal("dump changed")
	}
	clear(plain)
	for _, cut := range []int{0, 10, out.Len() / 2, out.Len() - 1} {
		if _, err = Verify(bytes.NewReader(out.Bytes()[:cut]), i.String(), keys); !errors.Is(err, ErrArchive) {
			t.Fatal("truncated archive accepted", cut, err)
		}
	}
	tampered := bytes.Clone(out.Bytes())
	tampered[len(tampered)-20] ^= 1
	if _, err = Verify(bytes.NewReader(tampered), i.String(), keys); !errors.Is(err, ErrArchive) {
		t.Fatal("tampered payload accepted")
	}
	wrong, _ := age.GenerateX25519Identity()
	if _, err = Verify(bytes.NewReader(out.Bytes()), wrong.String(), keys); !errors.Is(err, ErrArchive) {
		t.Fatal("wrong identity accepted")
	}
	missing := testKeys()
	delete(missing.MasterKeys, "master-old")
	defer missing.Clear()
	if _, err = Verify(bytes.NewReader(out.Bytes()), i.String(), missing); !errors.Is(err, ErrKeys) {
		t.Fatal("missing old master key accepted")
	}
	wrongMaterial := testKeys()
	wrongMaterial.TokenPepper[0] ^= 1
	defer wrongMaterial.Clear()
	if _, err = Verify(bytes.NewReader(out.Bytes()), i.String(), wrongMaterial); !errors.Is(err, ErrKeys) {
		t.Fatal("mismatched token key accepted")
	}
}
