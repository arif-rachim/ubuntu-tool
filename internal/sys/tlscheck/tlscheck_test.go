package tlscheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfSigned membuat sertifikat uji untuk nama tertentu dengan masa berlaku tertentu.
func selfSigned(t *testing.T, names []string, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func serve(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func TestCheckHostSelfSigned(t *testing.T) {
	now := time.Now()
	port := serve(t, selfSigned(t, []string{"localhost"}, now.Add(-time.Hour), now.Add(10*24*time.Hour)))
	res := CheckHost(context.Background(), "localhost", port, now)
	if res.Err != nil || res.Cert == nil {
		t.Fatalf("%+v", res)
	}
	if res.Valid() || !strings.Contains(res.Problem, "self-signed") {
		t.Errorf("self-signed harus bermasalah: %q", res.Problem)
	}
	if res.Cert.DaysLeft(now) != 9 && res.Cert.DaysLeft(now) != 10 {
		t.Errorf("sisa hari %d", res.Cert.DaysLeft(now))
	}
}

func TestExplainHostnameDanKedaluwarsa(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, []string{"contoh.com"}, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	_, err := leaf.Verify(x509.VerifyOptions{DNSName: "salah.com", Roots: pool, CurrentTime: now.Add(-36 * time.Hour)})
	if got := Explain(err, "salah.com"); !strings.Contains(got, "bukan untuk salah.com") || !strings.Contains(got, "contoh.com") {
		t.Errorf("hostname: %s", got)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: "contoh.com", Roots: pool, CurrentTime: now})
	if got := Explain(err, "contoh.com"); !strings.Contains(got, "kedaluwarsa") {
		t.Errorf("kedaluwarsa: %s", got)
	}
}

func TestCheckFileDanWarning(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, []string{"app.contoh.com"}, now.Add(-time.Hour), now.Add(5*24*time.Hour))
	path := filepath.Join(t.TempDir(), "fullchain.pem")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o644)
	res := CheckFile(path, now)
	if res.Err != nil || res.Cert.Subject != "app.contoh.com" || res.Problem != "" {
		t.Fatalf("%+v", res)
	}
	if w := res.Warning(now); !strings.Contains(w, "4 hari") && !strings.Contains(w, "5 hari") {
		t.Errorf("warning: %q", w)
	}
	if CheckFile(filepath.Join(t.TempDir(), "x"), now).Err == nil {
		t.Error("file tidak ada harus error")
	}
}
