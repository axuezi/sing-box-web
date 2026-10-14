package inbounds

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"time"

	"github.com/axuezi/sing-box-web/internal/protocol"
)

// RealityKeyPair returns a new X25519 key pair in the base64url form
// `sing-box generate reality-keypair` prints.
func RealityKeyPair() (private, public string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	// Clamp as X25519 does, so the stored key is canonical.
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	private = base64.RawURLEncoding.EncodeToString(k[:])
	public, err = protocol.RealityPublicKey(private)
	return private, public, err
}

// ShortID returns a random 8-byte Reality short id in hex.
func ShortID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// SelfSigned returns a PEM certificate and key for name (a host name or IP),
// valid for ten years. Clients must skip verification for it.
func SelfSigned(name string) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(name); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
	return certPEM, keyPEM, nil
}
