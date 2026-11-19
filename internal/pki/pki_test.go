package pki

import (
	"crypto/x509"
	"net"
	"testing"
)

func TestIssuedCertsVerifyAgainstCA(t *testing.T) {
	ca, err := NewCA("spo-test-ca")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := ca.Issue("spo-controller", []string{"spo-controller"}, []net.IP{net.ParseIP("10.0.0.2")}, x509.ExtKeyUsageServerAuth)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ca.Issue("node1", nil, nil, x509.ExtKeyUsageClientAuth)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := srv.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "spo-controller"}); err != nil {
		t.Fatalf("server cert: %v", err)
	}
	if _, err := cli.Leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client cert: %v", err)
	}
	if _, err := cli.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "spo-controller"}); err == nil {
		t.Fatal("client cert accepted as server cert")
	}
	if cli.Leaf.Subject.CommonName != "node1" || len(cli.CertPEM) == 0 || len(cli.KeyPEM) == 0 || len(ca.CertPEM()) == 0 {
		t.Fatal("missing PEM output or CN")
	}
}
