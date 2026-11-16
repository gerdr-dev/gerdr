package tailscale

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mac.tail1.ts.net"},
		DNSNames: []string{"mac.tail1.ts.net"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// Both files to "-" put the chain and then the key on stdout; split must hand back a pair tls accepts.
func TestSplit(t *testing.T) {
	c, k := selfSigned(t)
	gotC, gotK, err := split(append(append([]byte{}, c...), k...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(gotC, gotK); err != nil {
		t.Fatal(err)
	}
	if _, _, err := split(c); err == nil {
		t.Fatal("no key should be an error")
	}
}

func TestParse(t *testing.T) {
	self := &struct{ DNSName string }{"Mac.tail1.ts.net."}
	if in := parse(status{BackendState: "Running", Self: self, CertDomains: []string{"mac.tail1.ts.net"}}); in.Name != "mac.tail1.ts.net" || in.Problem != "" {
		t.Fatalf("ready: %+v", in)
	}
	if in := parse(status{BackendState: "Running", Self: self}); !strings.Contains(in.Problem, AdminDNS) {
		t.Fatalf("HTTPS off should point at the admin page: %+v", in)
	}
	if in := parse(status{BackendState: "NeedsLogin", Self: self, CertDomains: []string{"x"}}); in.Problem == "" {
		t.Fatalf("not connected: %+v", in)
	}
}

func TestExplain(t *testing.T) {
	if e := explain("Usage: tailscale cert [flags] <domain>\nHTTPS cert support is not enabled/configured for your tailnet."); !strings.Contains(e.Error(), AdminDNS) {
		t.Fatal(e)
	}
}
