package auth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMemoryAndLogSenders(t *testing.T) {
	m := &MemorySender{}
	ctx := context.Background()
	_ = m.SendVerification(ctx, "a@x.io", "https://app/verify?token=1")
	_ = m.SendPasswordReset(ctx, "a@x.io", "https://app/reset?token=2")
	if len(m.Sent()) != 2 {
		t.Fatal(m.Sent())
	}
	if last, ok := m.Last("reset", "a@x.io"); !ok || !strings.HasSuffix(last.Link, "token=2") {
		t.Errorf("%+v", last)
	}
	if _, ok := m.Last("reset", "b@x.io"); ok {
		t.Error("found mail for the wrong address")
	}
	var buf bytes.Buffer
	l := LogSender{Log: slog.New(slog.NewTextHandler(&buf, nil))}
	_ = l.SendVerification(ctx, "a@x.io", "L1")
	_ = l.SendPasswordReset(ctx, "a@x.io", "L2")
	if !strings.Contains(buf.String(), "L1") || !strings.Contains(buf.String(), "L2") {
		t.Error(buf.String())
	}
}

// fakeSMTP is a minimal SMTP server; with tlsCfg set it offers STARTTLS.
func fakeSMTP(t *testing.T, tlsCfg *tls.Config) (port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var c net.Conn = conn
		rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
		say := func(s string) { rw.WriteString(s + "\r\n"); rw.Flush() }
		say("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				got <- data.String()
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					say("250 ok")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO":
				if tlsCfg != nil {
					say("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
				} else {
					say("250 fake")
				}
			case "STARTTLS":
				say("220 go ahead")
				tc := tls.Server(c, tlsCfg)
				if err := tc.Handshake(); err != nil {
					return
				}
				c = tc
				rw = bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
			case "AUTH":
				say("235 ok")
			case "DATA":
				inData = true
				say("354 go")
			case "QUIT":
				say("221 bye")
				got <- data.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, pool
}

func TestSMTPSenderRequiresSTARTTLS(t *testing.T) {
	port, _ := fakeSMTP(t, nil)
	s := SMTPSender{Config: SMTPConfig{Host: "127.0.0.1", Port: port, From: "app@x.io", Timeout: 2 * time.Second}}
	err := s.SendVerification(context.Background(), "a@x.io", "L")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("plain-text server accepted: %v", err)
	}
}

func TestSMTPSenderSends(t *testing.T) {
	serverTLS, pool := selfSigned(t)
	port, got := fakeSMTP(t, serverTLS)
	s := SMTPSender{
		Config: SMTPConfig{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", From: "app@x.io"},
		TLS:    &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
	}
	if err := s.SendPasswordReset(context.Background(), "a@x.io", "https://app/reset?token=abc"); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	if !strings.Contains(msg, "Subject: Reset your password") || !strings.Contains(msg, "https://app/reset?token=abc") {
		t.Errorf("%q", msg)
	}

	for name, s := range map[string]SMTPSender{
		"no host":         {},
		"header inject":   {Config: SMTPConfig{Host: "127.0.0.1", Port: port, From: "a@x.io\r\nBcc: evil@x.io"}},
		"connect refused": {Config: SMTPConfig{Host: "127.0.0.1", Port: closedPort(t), From: "a@x.io"}},
	} {
		if err := s.SendVerification(context.Background(), "a@x.io", "L"); err == nil {
			t.Errorf("%s: sent", name)
		}
	}
}

func closedPort(t *testing.T) int {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}
