// Package localtls serves Slidekick over HTTPS on the local network, next to
// the plain-HTTP listener (which always stays up for reverse proxies and
// Chromecasts). The certificate is either one the admin provides or one
// signed by a small certificate authority Slidekick makes for itself. Nothing
// here talks to the internet: no ACME, no public DNS, no external services.
package localtls

import (
	"crypto"
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
	"net/netip"
	"strings"
	"time"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour // within every platform's limit for certificates from a user-trusted CA
	renewBefore    = 30 * 24 * time.Hour
)

// privateRanges are the only IP ranges the generated CA may vouch for.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// localSuffixes are the DNS names the generated CA may vouch for besides the
// names configured when it was made: names that never resolve on the internet.
var localSuffixes = []string{"local", "lan", "home.arpa", "internal", "localhost"}

// Names is a certificate's host names and IP addresses.
type Names struct {
	DNS []string
	IPs []net.IP
}

// ParseNames checks and splits host names and IP addresses. IP addresses must
// be private (LAN only); host names are plain names (no wildcards, no ports).
func ParseNames(in []string) (Names, error) {
	var n Names
	seen := map[string]bool{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		s = strings.TrimSuffix(s, ".")
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		if a, err := netip.ParseAddr(s); err == nil {
			if !isPrivate(a) {
				return Names{}, fmt.Errorf("%s isn't a private (LAN) address: local HTTPS is for the local network only", s)
			}
			n.IPs = append(n.IPs, net.IP(a.AsSlice()))
			continue
		}
		if !validDNSName(s) {
			return Names{}, fmt.Errorf("%q isn't a host name or IP address (no wildcards, ports or schemes)", raw)
		}
		n.DNS = append(n.DNS, s)
	}
	if len(n.DNS)+len(n.IPs) == 0 {
		return Names{}, errors.New("add at least one host name or IP address that people use to reach Slidekick")
	}
	if len(n.DNS)+len(n.IPs) > 32 {
		return Names{}, errors.New("at most 32 host names and addresses")
	}
	return n, nil
}

func isPrivate(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range privateRanges {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func validDNSName(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func pemOf(typ string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}

func encodeKey(k crypto.Signer) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", err
	}
	return pemOf("PRIVATE KEY", der), nil
}

// NewCA makes a certificate authority for this install. It is name-constrained:
// it can only vouch for private IP addresses, local-only DNS suffixes and the
// names given now, so a copy of its key can't be used to impersonate
// arbitrary internet sites to the devices that trust it.
func NewCA(names Names, now time.Time) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	permitted := append([]string{}, localSuffixes...)
	for _, d := range names.DNS {
		if !coveredBySuffix(d, localSuffixes) {
			permitted = append(permitted, d)
		}
	}
	var ranges []*net.IPNet
	for _, p := range privateRanges {
		ranges = append(ranges, &net.IPNet{IP: net.IP(p.Addr().AsSlice()), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())})
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Slidekick local CA", Organization: []string{"Slidekick"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		PermittedDNSDomains:   permitted,
		PermittedIPRanges:     ranges,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	kp, err := encodeKey(key)
	if err != nil {
		return "", "", err
	}
	return pemOf("CERTIFICATE", der), kp, nil
}

func coveredBySuffix(name string, suffixes []string) bool {
	for _, s := range suffixes {
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// ErrNotCovered means a name can't go on a certificate from the existing CA.
var ErrNotCovered = errors.New("not covered by this install's certificate authority")

// Issue signs a server certificate for names with the CA. The returned
// certificate PEM is the chain: the server certificate, then the CA.
func Issue(caCertPEM, caKeyPEM string, names Names, now time.Time) (certPEM, keyPEM string, err error) {
	ca, err := tls.X509KeyPair([]byte(caCertPEM), []byte(caKeyPEM))
	if err != nil {
		return "", "", fmt.Errorf("certificate authority: %w", err)
	}
	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return "", "", err
	}
	for _, d := range names.DNS {
		if !coveredBySuffix(d, caCert.PermittedDNSDomains) {
			return "", "", fmt.Errorf("%s is %w (it was made before this name was added): replace the certificate authority, then trust the new one on your devices", d, ErrNotCovered)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	cn := ""
	if len(names.DNS) > 0 {
		cn = names.DNS[0]
	} else {
		cn = names.IPs[0].String()
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names.DNS,
		IPAddresses:  names.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return "", "", err
	}
	kp, err := encodeKey(key)
	if err != nil {
		return "", "", err
	}
	return pemOf("CERTIFICATE", der) + caCertPEM, kp, nil
}

// CertInfo describes a certificate for the settings page.
type CertInfo struct {
	Subject     string    `json:"subject"`
	Names       []string  `json:"names"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"` // SHA-256 of the DER, hex with colons
}

func infoOf(c *x509.Certificate) CertInfo {
	names := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	sum := sha256.Sum256(c.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return CertInfo{Subject: c.Subject.CommonName, Names: names, NotAfter: c.NotAfter, Fingerprint: strings.Join(parts, ":")}
}

// CheckPair validates a certificate and key: they must match, parse, be
// valid now and be a server (not CA) certificate.
func CheckPair(certPEM, keyPEM string, now time.Time) (tls.Certificate, CertInfo, error) {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return tls.Certificate{}, CertInfo{}, fmt.Errorf("the certificate and key don't form a pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, CertInfo{}, err
	}
	switch {
	case now.Before(leaf.NotBefore):
		return tls.Certificate{}, CertInfo{}, fmt.Errorf("the certificate isn't valid until %s", leaf.NotBefore.Format(time.RFC3339))
	case now.After(leaf.NotAfter):
		return tls.Certificate{}, CertInfo{}, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.Format(time.RFC3339))
	case leaf.IsCA:
		return tls.Certificate{}, CertInfo{}, errors.New("that's a CA certificate: provide the server certificate (its CA may follow it in the same file)")
	}
	pair.Leaf = leaf
	return pair, infoOf(leaf), nil
}

// caInfo describes the generated CA.
func caInfo(caCertPEM string) (CertInfo, error) {
	b, _ := pem.Decode([]byte(caCertPEM))
	if b == nil {
		return CertInfo{}, errors.New("no CA certificate")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return CertInfo{}, err
	}
	return infoOf(c), nil
}
