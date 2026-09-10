package host

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodeCertificate is a certificate the service cannot obtain, read, or
// write. There is no fallback: an edge pins this key, so serving a different
// one than the edges were provisioned with would refuse every edge, and
// serving none would expose every assertion to whatever sits in the path.
var ErrCodeCertificate = errs.NewCode("host/certificate")

// certificateValidity is how long a generated pair is valid. It is long
// because rotating it is not a routine act: an edge pins this key for the
// life of its enrollment, so a expiry that comes round on its own would strand
// every edge in the field at a moment nobody chose.
const certificateValidity = 10 * 365 * 24 * time.Hour

// Certificate is what the service serves and what an edge is provisioned to
// accept: the same key on the API listener and the bus listener, so an edge
// pins one digest for both.
type Certificate struct {
	// TLS is the pair the listeners serve.
	TLS tls.Certificate
	// SPKI is the SHA-256 digest of the subject public key info, the anchor
	// EdgeProvisioning and EnrollResponse carry.
	SPKI []byte
}

// ObtainCertificate returns the pair this deployment serves.
//
// A deployment that names a certificate and key gets those. One that names
// neither gets a self-signed pair, generated on first start into the state
// directory and read back on every start after it.
//
// Persisting the pair is what makes a restart survivable. An edge accepts a
// chain only when its leaf's SPKI digest is one it was provisioned with, so a
// service that generated a fresh key each time it started would refuse every
// edge already in the field and there would be no way back except
// re-provisioning each one by hand.
func ObtainCertificate(cfg *Config, log *slog.Logger) (*Certificate, error) {
	if certificateFile, keyFile, supplied := cfg.CertificateFiles(); supplied {
		return loadCertificate(certificateFile, keyFile, log)
	}

	certificateFile := filepath.Join(cfg.StateDir(), "tls.crt")
	keyFile := filepath.Join(cfg.StateDir(), "tls.key")
	if _, err := os.Stat(certificateFile); err == nil {
		return loadCertificate(certificateFile, keyFile, log)
	} else if !os.IsNotExist(err) {
		return nil, errs.From(err).Code(ErrCodeCertificate).Attr("path", certificateFile).
			Msg("look for the persisted certificate")
	}
	// The state directory is this service's to create. Nothing else has
	// made it by now: Run obtains the certificate before the runtime starts
	// a single module, and the only other MkdirAll on this path is inside
	// the hub's key loading, which happens later. A packaged deployment
	// naming a directory that does not exist yet — the ordinary first start
	// — otherwise exits with a bare "no such file or directory" from
	// os.WriteFile, and both this doc and the store schema's README say the
	// service generates the pair into the state directory on first start.
	if err := os.MkdirAll(cfg.StateDir(), 0o700); err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Attr("path", cfg.StateDir()).
			Msg("create the state directory")
	}
	return generateCertificate(cfg, certificateFile, keyFile, log)
}

// loadCertificate reads a pair this deployment already has, whether the
// operator supplied it or a previous start generated it.
//
// It reports the certificate it is serving and deliberately not the digest of
// it. The digest is announced once, where the pair is created; restating it on
// every start gives an operator a value to copy from a line that may describe
// a file since replaced, and the file itself is where it should be recomputed.
func loadCertificate(certificateFile, keyFile string, log *slog.Logger) (*Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Attr("certificate", certificateFile).
			Msg("load the certificate and its key")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Attr("certificate", certificateFile).
			Msg("parse the served certificate")
	}
	pair.Leaf = leaf
	log.Info("service certificate loaded", slog.String("flowseer.device.certificate.path", certificateFile))
	return &Certificate{TLS: pair, SPKI: edgebus.SPKIDigest(leaf)}, nil
}

// generateCertificate writes a fresh self-signed pair and reports its digest.
//
// The digest is logged here and nowhere else, because this runs once in the
// life of a deployment. That is deliberate rather than thrifty: an operator
// provisioning an edge later recomputes it from the certificate file, and a
// digest restated on every start is one more line nobody reads and one more
// place it can be copied from while the file it describes has been replaced.
func generateCertificate(cfg *Config, certificateFile, keyFile string, log *slog.Logger) (*Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Msg("generate the service key")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Msg("draw a certificate serial")
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "flowseer-device-service"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certificateValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	addSubjectNames(template, cfg)

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Msg("sign the service certificate")
	}
	if err := writeCertificate(certificateFile, keyFile, der, key); err != nil {
		return nil, err
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCertificate).Msg("parse the certificate just written")
	}
	pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	digest := edgebus.SPKIDigest(leaf)

	log.Info("service certificate generated",
		slog.String("flowseer.device.certificate.spki_sha256", hex.EncodeToString(digest)),
		slog.String("flowseer.device.certificate.path", certificateFile),
		slog.String("flowseer.device.certificate.note",
			"provision every edge with this digest; it is logged only at generation and can be recomputed from the certificate file"),
	)
	return &Certificate{TLS: pair, SPKI: digest}, nil
}

// writeCertificate persists the pair, the key first and readable only by the
// service's own user.
//
// Order matters on a crash between the two. A key with no certificate is
// unreachable and the next start generates a fresh pair over it, which is
// correct because no edge was ever told about this one; a certificate with no
// key would be loaded on the next start and fail there, with an operator left
// to work out that a file they can see is useless.
func writeCertificate(certificateFile, keyFile string, der []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return errs.From(err).Code(ErrCodeCertificate).Msg("encode the service key")
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return errs.From(err).Code(ErrCodeCertificate).Attr("path", keyFile).Msg("write the service key")
	}

	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certificateFile, certificatePEM, 0o644); err != nil { //nolint:gosec // a served certificate is public
		return errs.From(err).Code(ErrCodeCertificate).Attr("path", certificateFile).
			Msg("write the service certificate")
	}
	return nil
}

// addSubjectNames names the certificate for the addresses this deployment
// binds, plus loopback so a local client reaches it without a name.
//
// An edge does not check these: it pins the public key and skips chain
// verification, which is what the pin is for. They are here for everything
// else that speaks to the service — an operator's browser, a probe, a proxy —
// which does check, and would otherwise need its own exception.
func addSubjectNames(template *x509.Certificate, cfg *Config) {
	template.DNSNames = []string{"localhost"}
	template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}

	for _, address := range []string{cfg.APIAddress(), cfg.BusAddress(), cfg.CentralURL()} {
		host := hostOf(address)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if !slices.ContainsFunc(template.IPAddresses, ip.Equal) {
				template.IPAddresses = append(template.IPAddresses, ip)
			}
			continue
		}
		if !slices.Contains(template.DNSNames, host) {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
}

// listenHostOf is the host a listener binds, which is not the host a
// certificate names. An empty result means "every interface" to a listener
// and "no dialable name" to a SAN, so the two cannot share a mapping:
// edgebus reads an empty ListenHost as loopback, and a deployment that wrote
// 0.0.0.0 would silently bind the bus to 127.0.0.1 while the API answered
// everywhere and AttachBus handed edges a routable address.
func listenHostOf(address string) string {
	// The configuration schema admits only host:port here, so the split
	// cannot fail; an empty host is the wildcard bind, which a listener
	// reads as every interface.
	host, _, _ := net.SplitHostPort(address)
	return host
}

// hostOf is the host part of a host:port or of a URL, empty for a wildcard
// bind, which names nothing a peer can dial.
func hostOf(address string) string {
	if parsed, err := url.Parse(address); err == nil && parsed.Host != "" {
		address = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	switch address {
	case "", "0.0.0.0", "::", "[::]":
		return ""
	}
	return address
}
