//go:build linux || darwin

package credential

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"

	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes, one per refused read.
var (
	ErrCodeInvalidKey      = errs.NewCode("credential/invalid-key")
	ErrCodeNotFound        = errs.NewCode("credential/not-found")
	ErrCodeSymlinkRefused  = errs.NewCode("credential/symlink-refused")
	ErrCodeNotRegular      = errs.NewCode("credential/not-regular")
	ErrCodeInsecureMode    = errs.NewCode("credential/insecure-mode")
	ErrCodeReadFailed      = errs.NewCode("credential/read-failed")
	ErrCodeInvalidMetadata = errs.NewCode("credential/invalid-metadata")
	ErrCodeVersionMismatch = errs.NewCode("credential/version-mismatch")
)

// keyPattern is the same one-path-segment shape
// flowseer.device.policy.v1.CredentialHandle.key requires: lowercase,
// digits, dot, underscore, and hyphen, starting with a letter or digit.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// metaSuffix names the sidecar file that carries a credential's declared
// version, read and validated after the material itself opens.
const metaSuffix = ".meta.json"

type credentialMeta struct {
	Version uint64 `json:"version"`
}

// Provider reads device credentials from files under one mounted root,
// opened once and held for the Provider's lifetime.
type Provider struct {
	root *os.File
}

// Open opens rootPath as the credential mount root. The caller must Close
// the returned Provider.
func Open(rootPath string) (*Provider, error) {
	root, err := os.Open(rootPath)
	if err != nil {
		return nil, errs.From(err).Msg("open credential mount root")
	}
	fi, err := root.Stat()
	if err != nil {
		_ = root.Close()
		return nil, errs.From(err).Msg("stat credential mount root")
	}
	if !fi.IsDir() {
		_ = root.Close()
		return nil, errs.New().Msgf("credential mount root %q is not a directory", rootPath)
	}
	return &Provider{root: root}, nil
}

// Close releases the held root descriptor.
func (p *Provider) Close() error {
	return p.root.Close()
}

// Get returns the credential material stored under key, after validating
// that its sidecar metadata declares wantVersion. key must be one path
// segment in the same shape a CredentialHandle's key takes; a key
// containing a path separator or a ".." segment is refused before any
// filesystem call.
func (p *Provider) Get(key string, wantVersion uint64) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	material, err := p.openNoFollow(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = material.Close() }()
	if err := checkSecure(material, key); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(material)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeReadFailed).Attr("key", key).Msg("read credential material")
	}

	metaFile, err := p.openNoFollow(key + metaSuffix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = metaFile.Close() }()
	if err := checkSecure(metaFile, key); err != nil {
		return nil, err
	}
	var meta credentialMeta
	if err := json.NewDecoder(metaFile).Decode(&meta); err != nil {
		return nil, errs.From(err).Code(ErrCodeInvalidMetadata).Attr("key", key).Msg("parse credential metadata")
	}
	if meta.Version != wantVersion {
		return nil, errs.New().Code(ErrCodeVersionMismatch).
			Attr("key", key).
			Attr("want_version", wantVersion).
			Attr("got_version", meta.Version).
			Msg("credential metadata version does not match")
	}

	return data, nil
}

func validateKey(key string) error {
	if key == "" || len(key) > 128 || !keyPattern.MatchString(key) {
		return errs.New().Code(ErrCodeInvalidKey).Attr("key", key).Msg("credential key is not a single, well-formed path segment")
	}
	return nil
}

// openNoFollow opens name relative to the held root descriptor with
// O_NOFOLLOW, so a symlink at that exact path is refused by the kernel
// call itself rather than by a check this code performs afterward.
func (p *Provider) openNoFollow(name string) (*os.File, error) {
	fd, err := unix.Openat(int(p.root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, errs.From(err).Code(ErrCodeSymlinkRefused).Attr("key", name).Msg("credential path is a symlink")
		}
		return nil, errs.From(err).Code(ErrCodeNotFound).Attr("key", name).Msg("open credential file")
	}
	return os.NewFile(uintptr(fd), name), nil
}

// checkSecure validates the already-open file's metadata: it is a regular
// file, and its mode grants no group or world access. It runs on the open
// file descriptor, so nothing it observes can be changed by a later swap
// of the directory entry.
func checkSecure(f *os.File, key string) error {
	fi, err := f.Stat()
	if err != nil {
		return errs.From(err).Code(ErrCodeReadFailed).Attr("key", key).Msg("stat open credential file")
	}
	if !fi.Mode().IsRegular() {
		return errs.New().Code(ErrCodeNotRegular).Attr("key", key).Msg("credential path did not resolve to a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return errs.New().Code(ErrCodeInsecureMode).Attr("key", key).Msg("credential file grants group or world access")
	}
	return nil
}
