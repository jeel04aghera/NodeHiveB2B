package tlsboot

import (
	"crypto/x509"
	"testing"
)

// The CA and per-boot server certificates must have the right shape: the CA can
// sign leaves only (no intermediates), and the leaf must verify against the CA for
// exactly the hosts the agent will dial.
func TestCAAndServerCert(t *testing.T) {
	certPEM, keyPEM, err := generateCA()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ca, err := parseCA(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ca.cert.IsCA || !ca.cert.MaxPathLenZero {
		t.Error("CA must be a path-length-zero CA (leaves only)")
	}
	if ca.FingerprintSHA256() == "" {
		t.Error("fingerprint must be non-empty")
	}

	tlsCfg, err := ca.ServerTLS([]string{"gw.proxy.rlwy.net", "10.0.0.5"})
	if err != nil {
		t.Fatalf("server tls: %v", err)
	}
	leaf, err := x509.ParseCertificate(tlsCfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	for _, host := range []string{"gw.proxy.rlwy.net", "10.0.0.5", "localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots: roots, DNSName: host,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Errorf("leaf must verify for %q: %v", host, err)
		}
	}
	// A host NOT in the SANs must fail verification (no wildcard sloppiness).
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, DNSName: "evil.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("leaf must NOT verify for a host outside its SANs")
	}
	// And verification against an unrelated CA must fail (pinning is real).
	otherPEM, otherKey, _ := generateCA()
	other, _ := parseCA(otherPEM, otherKey)
	otherRoots := x509.NewCertPool()
	otherRoots.AddCert(other.cert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: otherRoots, DNSName: "gw.proxy.rlwy.net",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("leaf must NOT verify against a different CA")
	}
}
