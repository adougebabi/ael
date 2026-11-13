package remote

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ReadAuthority accepts public certificate blocks only. A chosen server private
// key cannot accidentally become part of the stored client trust profile.
func ReadAuthority(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("REMOTE_CA_UNAVAILABLE")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", errors.New("REMOTE_CA_INVALID: certificate input exceeds its limit")
	}
	return canonicalAuthority(string(raw))
}

func validateAuthority(text string) error {
	_, err := canonicalAuthority(text)
	return err
}

func canonicalAuthority(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	if len(text) > 1<<20 {
		return "", errors.New("REMOTE_CA_INVALID: certificate input exceeds its limit")
	}
	rest := []byte(text)
	var certificates strings.Builder
	count := 0
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			if strings.Contains(string(rest), "-----BEGIN") {
				return "", errors.New("REMOTE_CA_INVALID: malformed PEM block")
			}
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return "", errors.New("REMOTE_CA_INVALID: use public certificate PEM only")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return "", errors.New("REMOTE_CA_INVALID: malformed public certificate")
		}
		certificates.Write(pem.EncodeToMemory(block))
		rest = remaining
		count++
	}
	if count == 0 {
		return "", errors.New("REMOTE_CA_INVALID: public certificates required")
	}
	return certificates.String(), nil
}

func NewHTTPClient(profile Profile) (*http.Client, error) {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if profile.CertificateAuthority == "" {
		return client, nil
	}
	if err := validateAuthority(profile.CertificateAuthority); err != nil {
		return nil, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM([]byte(profile.CertificateAuthority)) {
		return nil, errors.New("REMOTE_CA_INVALID")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	client.Transport = transport
	return client, nil
}
