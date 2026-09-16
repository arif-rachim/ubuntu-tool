// Package tlscheck memeriksa sertifikat TLS sebuah server atau file PEM dan menjelaskan masalahnya.
package tlscheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Cert adalah ringkasan satu sertifikat.
type Cert struct {
	Subject   string
	Issuer    string
	DNSNames  []string
	NotBefore time.Time
	NotAfter  time.Time
}

// DaysLeft adalah sisa hari sebelum kedaluwarsa (negatif bila sudah lewat).
func (c Cert) DaysLeft(now time.Time) int {
	return int(c.NotAfter.Sub(now).Hours() / 24)
}

// Result adalah hasil pemeriksaan.
type Result struct {
	Host    string
	Cert    *Cert
	Chain   int    // jumlah sertifikat yang dikirim server
	Problem string // penjelasan masalah dalam bahasa manusia; kosong bila valid
	Err     error  // error koneksi (bukan masalah sertifikat)
}

// Valid melaporkan sertifikat terverifikasi tanpa masalah.
func (r Result) Valid() bool { return r.Err == nil && r.Problem == "" && r.Cert != nil }

// Warning mengembalikan peringatan non-fatal (mis. hampir kedaluwarsa).
func (r Result) Warning(now time.Time) string {
	if r.Cert == nil || r.Problem != "" {
		return ""
	}
	switch d := r.Cert.DaysLeft(now); {
	case d < 7:
		return fmt.Sprintf("sertifikat kedaluwarsa dalam %d hari", d)
	case d < 30:
		return fmt.Sprintf("sertifikat kedaluwarsa dalam %d hari; pastikan perpanjangan otomatis berjalan", d)
	}
	return ""
}

func summarize(c *x509.Certificate) *Cert {
	return &Cert{
		Subject:   c.Subject.CommonName,
		Issuer:    firstNonEmpty(c.Issuer.CommonName, strings.Join(c.Issuer.Organization, ", ")),
		DNSNames:  c.DNSNames,
		NotBefore: c.NotBefore,
		NotAfter:  c.NotAfter,
	}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// Explain menerjemahkan error verifikasi sertifikat.
func Explain(err error, host string) string {
	var hostErr x509.HostnameError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &hostErr):
		return fmt.Sprintf("sertifikat bukan untuk %s (hanya berlaku untuk: %s)", host, strings.Join(hostErr.Certificate.DNSNames, ", "))
	case errors.As(err, &unknown):
		return "sertifikat tidak ditandatangani otoritas tepercaya (self-signed, atau server tidak mengirim sertifikat perantara/chain lengkap)"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "sertifikat sudah kedaluwarsa atau belum berlaku (periksa juga jam server)"
		}
		return "sertifikat tidak valid: " + invalid.Error()
	}
	return err.Error()
}

// CheckHost membuka koneksi TLS ke host:port dengan SNI host dan memverifikasi sertifikatnya.
func CheckHost(ctx context.Context, host, port string, now time.Time) Result {
	res := Result{Host: host}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // verifikasi dilakukan manual di bawah supaya sertifikat tetap bisa ditampilkan
		MinVersion:         tls.VersionTLS10,
	}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		res.Err = err
		return res
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		res.Problem = "server tidak mengirim sertifikat"
		return res
	}
	leaf := state.PeerCertificates[0]
	res.Cert, res.Chain = summarize(leaf), len(state.PeerCertificates)
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter, CurrentTime: now}); err != nil {
		res.Problem = Explain(err, host)
	}
	return res
}

// CheckFile membaca sertifikat PEM pertama dari file (mis. fullchain.pem Let's Encrypt).
func CheckFile(path string, now time.Time) Result {
	res := Result{Host: path}
	data, err := os.ReadFile(path)
	if err != nil {
		res.Err = err
		return res
	}
	var certs []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			certs = append(certs, c)
		}
	}
	if len(certs) == 0 {
		res.Err = errors.New("tidak ada sertifikat PEM di file ini")
		return res
	}
	res.Cert, res.Chain = summarize(certs[0]), len(certs)
	if now.After(certs[0].NotAfter) {
		res.Problem = "sertifikat sudah kedaluwarsa"
	}
	return res
}
