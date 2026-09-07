package trust

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreAddsListsAndRemovesCertificate(t *testing.T) {
	source := filepath.Join(t.TempDir(), "corporate.pem")
	certificatePEM, keyPEM := testCertificate(t)
	if err := os.WriteFile(source, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: filepath.Join(t.TempDir(), "certs")}
	added, err := store.Add(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || !strings.Contains(added[0].Subject, "Cassie Test CA") {
		t.Fatalf("unexpected certificate: %#v", added)
	}
	if !store.HasBundle() {
		t.Fatal("expected managed CA bundle")
	}
	info, err := os.Stat(store.Bundle())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle permissions = %v, err=%v", info.Mode().Perm(), err)
	}
	if _, err := store.Remove(added[0].Fingerprint[:12]); err != nil {
		t.Fatal(err)
	}
	if store.HasBundle() {
		t.Fatal("bundle should be removed with the final certificate")
	}

	privateSource := filepath.Join(t.TempDir(), "private.pem")
	if err := os.WriteFile(privateSource, append(certificatePEM, keyPEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(privateSource); err == nil || !strings.Contains(err.Error(), "private keys") {
		t.Fatalf("expected private-key rejection, got %v", err)
	}
}

func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Cassie Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certificate, private
}
