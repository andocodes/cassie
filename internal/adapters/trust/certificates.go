package trust

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Certificate struct {
	Path        string
	Subject     string
	Issuer      string
	Expires     time.Time
	Fingerprint string
}

type Store struct {
	Dir string
}

func (s Store) Add(source string) ([]Certificate, error) {
	content, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	certificates, err := parse(content)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create certificate directory: %w", err)
	}
	name := strings.ToLower(strings.ReplaceAll(certificates[0].Fingerprint, ":", "")) + ".pem"
	path := filepath.Join(s.Dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, fmt.Errorf("store certificate: %w", err)
	}
	for index := range certificates {
		certificates[index].Path = path
	}
	if err := s.rebuildBundle(); err != nil {
		return nil, err
	}
	return certificates, nil
}

func (s Store) List() ([]Certificate, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	var certificates []Certificate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".pem" || entry.Name() == "bundle.pem" {
			continue
		}
		path := filepath.Join(s.Dir, entry.Name())
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		parsed, parseErr := parse(content)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		for index := range parsed {
			parsed[index].Path = path
		}
		certificates = append(certificates, parsed...)
	}
	sort.Slice(certificates, func(i, j int) bool { return certificates[i].Subject < certificates[j].Subject })
	return certificates, nil
}

func (s Store) Remove(identifier string) (Certificate, error) {
	certificates, err := s.List()
	if err != nil {
		return Certificate{}, err
	}
	identifier = strings.ToLower(strings.ReplaceAll(identifier, ":", ""))
	for _, certificate := range certificates {
		fingerprint := strings.ToLower(strings.ReplaceAll(certificate.Fingerprint, ":", ""))
		base := strings.TrimSuffix(filepath.Base(certificate.Path), filepath.Ext(certificate.Path))
		if strings.HasPrefix(fingerprint, identifier) || strings.EqualFold(base, identifier) {
			if err := os.Remove(certificate.Path); err != nil {
				return Certificate{}, fmt.Errorf("remove certificate: %w", err)
			}
			if err := s.rebuildBundle(); err != nil {
				return Certificate{}, err
			}
			return certificate, nil
		}
	}
	return Certificate{}, fmt.Errorf("certificate %q was not found", identifier)
}

func (s Store) Bundle() string {
	return filepath.Join(s.Dir, "bundle.pem")
}

func (s Store) HasBundle() bool {
	info, err := os.Stat(s.Bundle())
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (s Store) rebuildBundle() error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".pem" && entry.Name() != "bundle.pem" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		if err := os.Remove(s.Bundle()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	var bundle strings.Builder
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			return err
		}
		bundle.Write(content)
		if !strings.HasSuffix(string(content), "\n") {
			bundle.WriteByte('\n')
		}
	}
	return os.WriteFile(s.Bundle(), []byte(bundle.String()), 0o600)
}

func parse(content []byte) ([]Certificate, error) {
	remaining := content
	var certificates []Certificate
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			if strings.TrimSpace(string(remaining)) != "" {
				return nil, fmt.Errorf("certificate file contains invalid PEM data")
			}
			break
		}
		remaining = rest
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return nil, fmt.Errorf("private keys are not accepted")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse X.509 certificate: %w", err)
		}
		if !certificate.IsCA || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, fmt.Errorf("certificate %q is not a certificate authority", certificate.Subject.String())
		}
		now := time.Now()
		if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
			return nil, fmt.Errorf("certificate %q is not currently valid", certificate.Subject.String())
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		certificates = append(certificates, Certificate{
			Subject:     certificate.Subject.String(),
			Issuer:      certificate.Issuer.String(),
			Expires:     certificate.NotAfter,
			Fingerprint: colonHex(fingerprint[:]),
		})
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("no X.509 certificates found")
	}
	return certificates, nil
}

func colonHex(value []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(value))
	parts := make([]string, 0, len(encoded)/2)
	for index := 0; index < len(encoded); index += 2 {
		parts = append(parts, encoded[index:index+2])
	}
	return strings.Join(parts, ":")
}
