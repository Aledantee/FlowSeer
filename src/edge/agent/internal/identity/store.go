package identity

import (
	"crypto/ed25519"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/prototext"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes this package returns. See each function's doc for when.
var (
	ErrCodeState  = errs.NewCode("agent/identity-state")
	ErrCodeEnroll = errs.NewCode("agent/enroll")
)

// Store is the agent's own state directory: one file holding the private key
// seed, one holding central's enrollment answer.
//
// Both are 0600 and the directory is 0700. The key file is the whole of this
// edge's identity — central holds only the public half — so an edge that
// loses it cannot be recovered by anything the edge itself can do.
type Store struct{ dir string }

// NewStore names the directory, creating it if it is not there. A packaged
// deployment's first start is the ordinary case where it is not.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Attr("path", dir).Msg("create the agent state directory")
	}
	return &Store{dir: dir}, nil
}

func (s *Store) keyPath() string        { return filepath.Join(s.dir, "edge.key") }
func (s *Store) enrollmentPath() string { return filepath.Join(s.dir, "enrollment.textproto") }

// LoadKey returns the persisted private key, or nil when none is written yet.
func (s *Store) LoadKey() (ed25519.PrivateKey, error) {
	seed, err := os.ReadFile(s.keyPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Attr("path", s.keyPath()).Msg("read the agent key")
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errs.New().Code(ErrCodeState).Attr("path", s.keyPath()).
			Msgf("agent key is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SaveKey writes the private key's seed. It is written whole or not at all:
// a torn key file is indistinguishable from a wrong one at the next start,
// and both are unrecoverable, so the rename below is what makes a crash
// during this write leave either the old key or none.
func (s *Store) SaveKey(key ed25519.PrivateKey) error {
	return s.writeAtomically(s.keyPath(), key.Seed())
}

// LoadEnrollment returns central's persisted answer, or nil when the edge has
// not recorded one. A key with no enrollment is the ordinary state after a
// crash between the two writes, and means "enroll again".
func (s *Store) LoadEnrollment() (*edgev1.EnrollResponse, error) {
	body, err := os.ReadFile(s.enrollmentPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Attr("path", s.enrollmentPath()).
			Msg("read the persisted enrollment")
	}
	msg := &edgev1.EnrollResponse{}
	if err := prototext.Unmarshal(body, msg); err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Attr("path", s.enrollmentPath()).
			Msg("parse the persisted enrollment")
	}
	return msg, nil
}

// SaveEnrollment writes central's answer verbatim.
//
// The whole response is kept rather than the fields the agent reads today.
// It is what central said, and an agent that stored a summary would have to
// re-enroll to learn anything it had not thought to keep — which it cannot
// do once the setup key is consumed.
func (s *Store) SaveEnrollment(response *edgev1.EnrollResponse) error {
	body, err := prototext.MarshalOptions{Multiline: true}.Marshal(response)
	if err != nil {
		return errs.From(err).Code(ErrCodeState).Msg("encode the enrollment answer")
	}
	return s.writeAtomically(s.enrollmentPath(), body)
}

// writeAtomically writes through a temporary file in the same directory and
// renames it into place, so a crash mid-write leaves the previous contents
// rather than a truncated file.
func (s *Store) writeAtomically(path string, body []byte) error {
	temp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("create a temporary file")
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("write the state file")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("flush the state file")
	}
	if err := temp.Close(); err != nil {
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("close the state file")
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("set the state file mode")
	}
	if err := os.Rename(name, path); err != nil {
		return errs.From(err).Code(ErrCodeState).Attr("path", path).Msg("move the state file into place")
	}
	return nil
}
