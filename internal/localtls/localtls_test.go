package localtls

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/secret"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Setting(_ context.Context, k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k]
}

func (s *memStore) SetSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func newManager(t *testing.T) *Manager {
	t.Helper()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{Store: &memStore{m: map[string]string{}}, Box: box, Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello over https") })}
	t.Cleanup(m.Close)
	return m
}

func pool(t *testing.T, caPEM string) *x509.CertPool {
	t.Helper()
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM([]byte(caPEM)) {
		t.Fatal("bad CA PEM")
	}
	return p
}

func leafOf(t *testing.T, chainPEM string) *x509.Certificate {
	t.Helper()
	b, _ := pem.Decode([]byte(chainPEM))
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseNames(t *testing.T) {
	n, err := ParseNames([]string{" Slides.Local ", "192.168.1.10", "slides.local", "fd00::5", ""})
	if err != nil || len(n.DNS) != 1 || n.DNS[0] != "slides.local" || len(n.IPs) != 2 {
		t.Fatalf("%+v %v", n, err)
	}
	for _, bad := range [][]string{{}, {"8.8.8.8"}, {"*.example.com"}, {"https://slides.local"}, {"slides.local:7443"}} {
		if _, err := ParseNames(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGeneratedChainVerifiesAndIsConstrained(t *testing.T) {
	now := time.Now()
	names, _ := ParseNames([]string{"192.168.1.10", "slides.example.com", "slides.lan"})
	caCert, caKey, err := NewCA(names, now)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := Issue(caCert, caKey, names, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CheckPair(cert, key, now); err != nil {
		t.Fatal(err)
	}
	leaf := leafOf(t, cert)
	for _, host := range []string{"192.168.1.10", "slides.example.com", "slides.lan"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(t, caCert), DNSName: host, CurrentTime: now}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	if leaf.NotAfter.Sub(now) > 398*24*time.Hour {
		t.Errorf("server certificate valid too long: %v", leaf.NotAfter)
	}

	// The CA can't be used for names outside its constraints: a certificate
	// for a public name made with its key fails verification.
	ca, _ := tls.X509KeyPair([]byte(caCert), []byte(caKey))
	caX, _ := x509.ParseCertificate(ca.Certificate[0])
	tmpl := *leaf
	tmpl.DNSNames, tmpl.IPAddresses = []string{"www.bank.example"}, nil
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caX, leaf.PublicKey, ca.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	evil, _ := x509.ParseCertificate(der)
	if _, err := evil.Verify(x509.VerifyOptions{Roots: pool(t, caCert), DNSName: "www.bank.example", CurrentTime: now}); err == nil {
		t.Fatal("the local CA vouched for a public name")
	}

	// A new name outside the constraints needs a new CA.
	more, _ := ParseNames([]string{"slides.other.example"})
	if _, _, err := Issue(caCert, caKey, more, now); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("want ErrNotCovered, got %v", err)
	}
	// Local-only suffixes are always covered.
	local, _ := ParseNames([]string{"kiosk.home.arpa", "10.1.2.3"})
	if _, _, err := Issue(caCert, caKey, local, now); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPairRejects(t *testing.T) {
	now := time.Now()
	names, _ := ParseNames([]string{"slides.lan"})
	caCert, caKey, _ := NewCA(names, now)
	cert, key, _ := Issue(caCert, caKey, names, now)
	_, otherKey, _ := Issue(caCert, caKey, names, now)
	if _, _, err := CheckPair(cert, otherKey, now); err == nil {
		t.Error("mismatched key accepted")
	}
	if _, _, err := CheckPair(cert, key, now.Add(400*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired certificate: %v", err)
	}
	if _, _, err := CheckPair(caCert, caKey, now); err == nil {
		t.Error("CA certificate accepted as a server certificate")
	}
	if _, _, err := CheckPair("junk", "junk", now); err == nil {
		t.Error("junk accepted")
	}
}

func TestManagerServesSwitchesAndRenews(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()

	// Off by default: nothing listens, nothing to download.
	if st := m.Status(ctx); st.Enabled || st.Listening || st.Mode != ModeGenerate {
		t.Fatalf("default %+v", st)
	}
	if _, ok := m.CACert(ctx); ok {
		t.Fatal("CA offered before one exists")
	}
	// Turning it on needs names.
	if _, err := m.Save(ctx, Update{Config: Config{Enabled: true}}); !errors.Is(err, ErrInput) {
		t.Fatalf("no names: %v", err)
	}
	st, err := m.Save(ctx, Update{Config: Config{Enabled: true, Names: []string{"127.0.0.1", "slides.lan"}}})
	if err != nil || !st.Listening || st.CA == nil || st.Cert == nil {
		t.Fatalf("enable: %+v %v", st, err)
	}
	if strings.Contains(m.Store.Setting(ctx, keyCAKey), "PRIVATE KEY") || strings.Contains(m.Store.Setting(ctx, keyKey), "PRIVATE KEY") {
		t.Fatal("private keys stored unencrypted")
	}
	caPEM, ok := m.CACert(ctx)
	if !ok {
		t.Fatal("no CA to download")
	}
	var clock func() time.Time // the client's clock (nil: now)
	get := func() (string, error) {
		c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool(t, caPEM), Time: clock}}}
		res, err := c.Get("https://" + m.BoundAddr() + "/")
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b), nil
	}
	if body, err := get(); err != nil || body != "hello over https" {
		t.Fatalf("https: %q %v", body, err)
	}
	firstCert := st.Cert.Fingerprint

	// Saving the same names keeps the certificate; the CA stays.
	st, _ = m.Save(ctx, Update{Config: Config{Enabled: true, Names: []string{"slides.lan", "127.0.0.1"}}})
	if st.Cert.Fingerprint != firstCert {
		t.Fatal("certificate reissued without a reason")
	}
	// A month before expiry, Apply renews it with the same CA.
	m.Now = func() time.Time { return time.Now().Add(380 * 24 * time.Hour) }
	if err := m.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(ctx); st.Cert.Fingerprint == firstCert || st.CA.Fingerprint == "" {
		t.Fatal("certificate not renewed")
	}
	clock = m.Now // the renewed certificate is served (and valid at that time)
	if _, err := get(); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
	m.Now, clock = nil, nil

	// Replacing the CA makes a new one.
	oldCA := m.Status(ctx).CA.Fingerprint
	if st, err := m.RotateCA(ctx); err != nil || st.CA.Fingerprint == oldCA {
		t.Fatalf("rotate: %+v %v", st, err)
	}

	// Off: the listener stops; plain settings survive.
	addr := m.BoundAddr()
	st, err = m.Save(ctx, Update{Config: Config{Enabled: false, Names: []string{"slides.lan"}}})
	if err != nil || st.Listening || m.BoundAddr() != "" {
		t.Fatalf("disable: %+v %v", st, err)
	}
	c := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if _, err := c.Get("https://" + addr + "/"); err == nil {
		t.Fatal("still serving after being switched off")
	}
}

func TestManagerProvidedCertificate(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	if _, err := m.Save(ctx, Update{Config: Config{Enabled: true, Mode: ModeProvided}}); !errors.Is(err, ErrInput) {
		t.Fatalf("provided without a certificate: %v", err)
	}
	names, _ := ParseNames([]string{"127.0.0.1"})
	caCert, caKey, _ := NewCA(names, time.Now())
	cert, key, _ := Issue(caCert, caKey, names, time.Now())
	if _, err := m.Save(ctx, Update{Config: Config{Enabled: true, Mode: ModeProvided}, CertPEM: cert, KeyPEM: "nope"}); !errors.Is(err, ErrInput) {
		t.Fatalf("bad key: %v", err)
	}
	if st := m.Status(ctx); st.Enabled || st.Listening {
		t.Fatal("a rejected save changed something")
	}
	st, err := m.Save(ctx, Update{Config: Config{Enabled: true, Mode: ModeProvided}, CertPEM: cert, KeyPEM: key})
	if err != nil || !st.Listening || st.CA != nil {
		t.Fatalf("provided: %+v %v", st, err)
	}
	if _, ok := m.CACert(ctx); ok {
		t.Fatal("a provided certificate has no CA to offer")
	}
	// Saving again without pasting keeps the stored pair.
	if st, err := m.Save(ctx, Update{Config: Config{Enabled: true, Mode: ModeProvided}}); err != nil || !st.Listening {
		t.Fatalf("resave: %+v %v", st, err)
	}
	// A fresh manager over the same store picks it up at startup.
	m2 := &Manager{Store: m.Store, Box: m.Box, Addr: "127.0.0.1:0", Handler: m.Handler}
	defer m2.Close()
	if err := m2.Apply(ctx); err != nil || m2.BoundAddr() == "" {
		t.Fatalf("startup apply: %v", err)
	}
}

func TestPortInUseChangesNothing(t *testing.T) {
	a := newManager(t)
	ctx := context.Background()
	if _, err := a.Save(ctx, Update{Config: Config{Enabled: true, Names: []string{"127.0.0.1"}}}); err != nil {
		t.Fatal(err)
	}
	b := newManager(t)
	b.Addr = a.BoundAddr()
	if _, err := b.Save(ctx, Update{Config: Config{Enabled: true, Names: []string{"127.0.0.1"}}}); err == nil || !strings.Contains(err.Error(), "can't serve HTTPS") {
		t.Fatalf("busy port: %v", err)
	}
	if b.Config(ctx).Enabled {
		t.Fatal("stored as enabled although it couldn't listen")
	}
}
