//go:build linux || darwin

package credential

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"

	"buf.build/go/protovalidate"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/encoding/prototext"

	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes, one per refused read.
var (
	ErrCodeInvalidKey        = errs.NewCode("credential/invalid-key")
	ErrCodeNotFound          = errs.NewCode("credential/not-found")
	ErrCodeSymlinkRefused    = errs.NewCode("credential/symlink-refused")
	ErrCodeNotRegular        = errs.NewCode("credential/not-regular")
	ErrCodeInsecureMode      = errs.NewCode("credential/insecure-mode")
	ErrCodeReadFailed        = errs.NewCode("credential/read-failed")
	ErrCodeInvalidMetadata   = errs.NewCode("credential/invalid-metadata")
	ErrCodeVersionMismatch   = errs.NewCode("credential/version-mismatch")
	ErrCodeRotatedDuringRead = errs.NewCode("credential/rotated-during-read")
	ErrCodeInvalidMaterial   = errs.NewCode("credential/invalid-material")
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
// opened once and held for the Provider's lifetime. Get is safe to call
// concurrently with other calls to Get; it is not safe to call Close
// concurrently with an in-flight Get, which may then race the descriptor
// Close releases.
type Provider struct {
	root *os.File

	// afterMaterialRead, when non-nil, runs immediately after the material
	// file's content is read and before the metadata file opens. Tests use
	// it to inject a rotation into that window; production leaves it nil.
	afterMaterialRead func()
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

// Get returns the credential material stored under key, parsed from the
// prototext file into a CredentialMaterial, after validating that its sidecar
// metadata declares wantVersion. The file holds the whole message — user name
// and protocol identifiers included — so a rotation updates one place. key must
// be one path segment in the same shape a CredentialHandle's key takes; a key
// containing a path separator or a ".." segment is refused before any
// filesystem call. The material is parsed only after every security, version,
// and rotation check passes, so those failures are reported over a parse error.
func (p *Provider) Get(key string, wantVersion uint64) (*credentialv1.CredentialMaterial, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	material, err := p.openNoFollow(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = material.Close() }()
	materialInfo, err := checkSecure(material, key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(material)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeReadFailed).Attr("key", key).Msg("read credential material")
	}
	if p.afterMaterialRead != nil {
		p.afterMaterialRead()
	}

	metaFile, err := p.openNoFollow(key + metaSuffix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = metaFile.Close() }()
	if _, err := checkSecure(metaFile, key); err != nil {
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

	// The material and the metadata were read through two independent
	// opens; a rotation landing between them could otherwise pair stale
	// material with the new metadata's version. Re-resolve the material
	// entry and refuse if it no longer identifies the file already read.
	recheck, err := p.openNoFollow(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = recheck.Close() }()
	recheckInfo, err := checkSecure(recheck, key)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(materialInfo, recheckInfo) {
		return nil, errs.New().Code(ErrCodeRotatedDuringRead).Attr("key", key).Msg("credential material rotated while its metadata was being read")
	}

	parsed := &credentialv1.CredentialMaterial{}
	if err := prototext.Unmarshal(data, parsed); err != nil {
		return nil, errs.From(err).Code(ErrCodeInvalidMaterial).Attr("key", key).Msg("parse credential material prototext")
	}
	if err := protovalidate.Validate(parsed); err != nil {
		return nil, errs.From(err).Code(ErrCodeInvalidMaterial).Attr("key", key).Msg("credential material fails its schema rules")
	}
	return parsed, nil
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
		switch {
		case errors.Is(err, unix.ELOOP):
			return nil, errs.From(err).Code(ErrCodeSymlinkRefused).Attr("key", name).Msg("credential path is a symlink")
		case errors.Is(err, unix.ENOENT):
			return nil, errs.From(err).Code(ErrCodeNotFound).Attr("key", name).Msg("credential file does not exist")
		default:
			return nil, errs.From(err).Code(ErrCodeReadFailed).Attr("key", name).Msg("open credential file")
		}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// checkSecure validates the already-open file's metadata: it is a regular
// file, and its mode grants no group or world access. It runs on the open
// file descriptor, so nothing it observes can be changed by a later swap
// of the directory entry, and it returns that descriptor's os.FileInfo so
// a caller can later confirm a re-opened path still names the same file.
func checkSecure(f *os.File, key string) (os.FileInfo, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeReadFailed).Attr("key", key).Msg("stat open credential file")
	}
	if !fi.Mode().IsRegular() {
		return nil, errs.New().Code(ErrCodeNotRegular).Attr("key", key).Msg("credential path did not resolve to a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, errs.New().Code(ErrCodeInsecureMode).Attr("key", key).Msg("credential file grants group or world access")
	}
	return fi, nil
}
