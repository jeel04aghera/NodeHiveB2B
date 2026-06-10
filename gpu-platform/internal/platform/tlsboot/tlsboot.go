// Package tlsboot provisions the agent-gateway TLS material for deployments where
// a public-PKI certificate is impossible (Railway TCP proxy hosts live under
// *.proxy.rlwy.net, a domain the operator cannot get Let's Encrypt to issue for).
//
// Model: a self-signed CA is generated once and persisted in Postgres (surviving
// container redeploys); a short-lived server certificate is minted from it at every
// boot with the current public hostnames as SANs. Agents pin the CA — they fetch it
// over the control plane's HTTPS edge (authenticated by WebPKI), which is the trust
// bootstrap: HTTPS vouches for the CA, the CA vouches for the gRPC endpoint.
package tlsboot

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CA is the loaded transport certificate authority.
type CA struct {
	CertPEM []byte
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
}

const caValidity = 10 * 365 * 24 * time.Hour
const leafValidity = 2 * 365 * 24 * time.Hour // re-minted every boot; generous so long-running processes don't expire

// EnsureCA loads the persisted transport CA, generating and storing one on first
// boot. Safe under concurrent startup (advisory lock + idempotent insert).
func EnsureCA(ctx context.Context, pool *pgxpool.Pool) (*CA, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('nodehive_transport_ca'))`); err != nil {
		return nil, fmt.Errorf("ca lock: %w", err)
	}

	var certPEM, keyPEM string
	err = tx.QueryRow(ctx, `SELECT cert_pem, key_pem FROM transport_ca WHERE id=1`).Scan(&certPEM, &keyPEM)
	if errors.Is(err, pgx.ErrNoRows) {
		c, k, genErr := generateCA()
		if genErr != nil {
			return nil, genErr
		}
		certPEM, keyPEM = string(c), string(k)
		if _, err := tx.Exec(ctx,
			`INSERT INTO transport_ca (id, cert_pem, key_pem) VALUES (1, $1, $2)`, certPEM, keyPEM); err != nil {
			return nil, fmt.Errorf("persist ca: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("load ca: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return parseCA([]byte(certPEM), []byte(keyPEM))
}

// ServerTLS mints a fresh server certificate signed by the CA for the given hosts
// (DNS names or IPs) and returns a ready gRPC server TLS config.
func (ca *CA) ServerTLS(hosts []string) (*tls.Config, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: "nodehive-agent-gateway"},
		NotBefore:    time.Now().Add(-5 * time.Minute), // clock-skew slack
		NotAfter:     time.Now().Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	seen := map[string]bool{}
	for _, h := range append(hosts, "localhost", "127.0.0.1") {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("mint server cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13, // matches the agent's pinned-CA client config
	}, nil
}

// FingerprintSHA256 is the CA certificate's SHA-256 fingerprint (hex), surfaced in
// the CA distribution endpoint so operators can verify out-of-band.
func (ca *CA) FingerprintSHA256() string {
	sum := sha256.Sum256(ca.cert.Raw)
	return hex.EncodeToString(sum[:])
}

func generateCA() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "NodeHive Transport CA", Organization: []string{"NodeHive"}},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		MaxPathLenZero:        true, // CA may sign leaves only, never intermediates
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ca: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, errors.New("transport_ca: invalid cert PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ca cert: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, errors.New("transport_ca: invalid key PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ca key: %w", err)
	}
	return &CA{CertPEM: certPEM, cert: cert, key: key}, nil
}

func randomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}
