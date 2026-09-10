package edgebus

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// ErrCodeKeys identifies a failure loading, creating, or using the hub's
// operator and account keys.
var ErrCodeKeys = errs.NewCode("edgebus/keys")

// hubKeys are the key pairs an operator-mode hub needs. Isolation is by
// account, and it goes one level further than the journal: a JetStream
// flow-control control message names its own reply subject, and the server
// obeys it with an internal client subject to no permissions, so an edge
// credential can make the server reflect a publish into any stream, and any
// JetStream API, present in its own account. The central account (the
// journal buckets and the audit stream) is one boundary; a separate account
// per edge is the second, so one edge's reflection cannot address another
// edge's JetStream API to delete its buffer. The operator, system, and
// central keys are generated on first start; a per-edge account key is
// generated when the edge first attaches and read back afterwards, so a
// restart keeps every minted credential valid.
type hubKeys struct {
	dir      string
	operator nkeys.KeyPair
	system   nkeys.KeyPair
	central  nkeys.KeyPair

	mu   sync.Mutex
	edge map[string]nkeys.KeyPair
}

func loadOrCreateKeys(dir string) (*hubKeys, error) {
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("create keys directory")
	}
	operator, err := loadOrCreateKey(filepath.Join(keysDir, "operator.nk"), nkeys.CreateOperator)
	if err != nil {
		return nil, err
	}
	system, err := loadOrCreateKey(filepath.Join(keysDir, "system.nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	central, err := loadOrCreateKey(filepath.Join(keysDir, "central.nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	return &hubKeys{dir: keysDir, operator: operator, system: system, central: central, edge: map[string]nkeys.KeyPair{}}, nil
}

// persistedEdgeIDs lists the edges whose account keys are on disk, so a
// restarted hub re-attaches every edge it had sourced.
func (k *hubKeys) persistedEdgeIDs() ([]string, error) {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("read keys directory")
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "edge-") && strings.HasSuffix(name, ".nk") {
			ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(name, "edge-"), ".nk"))
		}
	}
	return ids, nil
}

// validEdgeID reports whether id is safe to use as a file name component
// and as a JetStream stream and subject token.
//
// Checked before anything is written, because the two failures compound: a
// name nats-server rejects leaves a seed file behind that every later
// StartHub re-reads and fails on again, and a separator in it escapes the
// keys directory entirely.
func validEdgeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// edgeAccountKey returns the account key for one edge, creating and
// persisting it on first use so a restarted hub signs the same account and
// every credential minted under it stays valid.
func (k *hubKeys) edgeAccountKey(edgeID string) (nkeys.KeyPair, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if pair, ok := k.edge[edgeID]; ok {
		return pair, nil
	}
	pair, err := loadOrCreateKey(filepath.Join(k.dir, "edge-"+edgeID+".nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	k.edge[edgeID] = pair
	return pair, nil
}

func loadOrCreateKey(path string, create func() (nkeys.KeyPair, error)) (nkeys.KeyPair, error) {
	seed, err := os.ReadFile(path)
	switch {
	case err == nil:
		pair, err := nkeys.FromSeed(seed)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Attr("path", path).Msg("parse stored key seed")
		}
		return pair, nil
	case os.IsNotExist(err):
		pair, err := create()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Msg("create key")
		}
		seed, err := pair.Seed()
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Msg("read new key seed")
		}
		if err := writeSecretFile(path, seed); err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Attr("path", path).Msg("store key seed")
		}
		return pair, nil
	default:
		return nil, errs.From(err).Code(ErrCodeKeys).Attr("path", path).Msg("read key seed")
	}
}

// secretTempPrefix names the temporary file writeSecretFile renames from. It
// is fixed rather than arbitrary so the next write can recognize and remove
// one a crash left behind.
const secretTempPrefix = ".secret-"

// writeSecretFile writes body to path at mode 0600, atomically, and removes
// any temporary file an interrupted earlier write left in the directory.
//
// os.WriteFile applies its mode only when it creates the file, so a seed or
// a credential left by an earlier run under a different umask keeps whatever
// mode it had. The rename makes a crash mid-write leave the previous content
// instead of a short one — a truncated seed is permanent, since nothing
// rewrites it — and the directory sync makes the rename durable. Writers to
// one directory must be serialized: the sweep cannot tell a concurrent
// writer's temporary file from a leftover.
func writeSecretFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	leftovers, _ := filepath.Glob(filepath.Join(dir, secretTempPrefix+"*"))
	for _, leftover := range leftovers {
		// Best effort: a leftover that cannot be removed is a file the
		// caller has no way to act on, and refusing the write over it would
		// keep the hub or the leaf from starting at all.
		_ = os.Remove(leftover)
	}

	temp, err := os.CreateTemp(dir, secretTempPrefix+"*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = handle.Close() }()
	return handle.Sync()
}

func publicKey(pair nkeys.KeyPair) (string, error) {
	pub, err := pair.PublicKey()
	if err != nil {
		return "", errs.From(err).Code(ErrCodeKeys).Msg("read public key")
	}
	return pub, nil
}

// operatorJWT signs the operator claims naming the system account, which is
// what lets the server run JetStream in operator mode.
func (k *hubKeys) operatorJWT() (*jwt.OperatorClaims, error) {
	operatorPub, err := publicKey(k.operator)
	if err != nil {
		return nil, err
	}
	systemPub, err := publicKey(k.system)
	if err != nil {
		return nil, err
	}
	claims := jwt.NewOperatorClaims(operatorPub)
	claims.SystemAccount = systemPub
	encoded, err := claims.Encode(k.operator)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("encode operator claims")
	}
	decoded, err := jwt.DecodeOperatorClaims(encoded)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("decode operator claims")
	}
	return decoded, nil
}

// accountJWT signs one account's claims. diskBytes over zero gives the
// account a JetStream disk budget, the per-account ceiling that keeps one
// account's volume from starving the other; zero leaves JetStream off.
func (k *hubKeys) accountJWT(account nkeys.KeyPair, name string, diskBytes int64) (string, error) {
	pub, err := publicKey(account)
	if err != nil {
		return "", err
	}
	claims := jwt.NewAccountClaims(pub)
	claims.Name = name
	if diskBytes > 0 {
		claims.Limits.JetStreamLimits = jwt.JetStreamLimits{
			MemoryStorage: jwt.NoLimit,
			DiskStorage:   diskBytes,
			Streams:       jwt.NoLimit,
			Consumer:      jwt.NoLimit,
		}
	}
	encoded, err := claims.Encode(k.operator)
	if err != nil {
		return "", errs.From(err).Code(ErrCodeKeys).Attr("account", name).Msg("encode account claims")
	}
	return encoded, nil
}

// EdgeCredentials is what AttachBus hands an edge: the edge account's JWT,
// the user JWT the leaf authenticates with, and the seed that signs the
// server's nonce.
type EdgeCredentials struct {
	AccountJWT string
	// UserJWT is the bearer credential the leaf authenticates with, and
	// Seed the nkey private seed that signs the server's nonce. Both are
	// secret material: an exported raw field holding either renders in full
	// through fmt, slog and JSON.
	UserJWT secret.Value
	Seed    secret.Value
}

// CredsFile renders the user JWT and seed in the .creds layout a leaf
// remote reads.
func (c EdgeCredentials) CredsFile() ([]byte, error) {
	creds, err := jwt.FormatUserConfig(c.UserJWT.RevealString(), c.Seed.Reveal())
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("format user credentials")
	}
	return creds, nil
}

// mintUser signs a user in account with the given permissions and returns
// it alongside that account's JWT. accountJWT is the encoded account the
// user belongs to, so a caller need not re-encode it.
func (k *hubKeys) mintUser(account nkeys.KeyPair, accountJWT, name string, permissions jwt.Permissions) (EdgeCredentials, error) {
	user, err := nkeys.CreateUser()
	if err != nil {
		return EdgeCredentials{}, errs.From(err).Code(ErrCodeKeys).Msg("create user key")
	}
	userPub, err := publicKey(user)
	if err != nil {
		return EdgeCredentials{}, err
	}
	seed, err := user.Seed()
	if err != nil {
		return EdgeCredentials{}, errs.From(err).Code(ErrCodeKeys).Msg("read user seed")
	}
	claims := jwt.NewUserClaims(userPub)
	claims.Name = name
	claims.Permissions = permissions
	encoded, err := claims.Encode(account)
	if err != nil {
		return EdgeCredentials{}, errs.From(err).Code(ErrCodeKeys).Attr("user", name).Msg("encode user claims")
	}
	return EdgeCredentials{AccountJWT: accountJWT, UserJWT: secret.NewString(encoded), Seed: secret.New(seed)}, nil
}

// edgePermissions is the minimal set that lets the hub source an edge's
// buffer and nothing more. Publish: the edge's own subtree, and the
// $JSC.R reply subjects the hub's source client attaches to its consumer
// request. Subscribe: the edge domain's JetStream API, so that request
// reaches the edge, and the $JS.FC flow-control replies the sourcing
// consumer sends. The edge is not granted publish on its own JetStream
// API (nothing crosses the link needs it, and it is what would let a
// caller read the source consumer's delivery subject) nor any _INBOX
// subject (the stock random inbox prefix matches none of these and an
// account-wide _INBOX grant would reach central's own request replies).
func edgePermissions(edgeID string) jwt.Permissions {
	subtree := EdgeSubtree(DefaultTenant, edgeID) + ".>"
	api := "$JS." + EdgeDomain(edgeID) + ".API.>"
	return jwt.Permissions{
		Pub: jwt.Permission{Allow: jwt.StringList{subtree, "$JSC.R.>"}},
		Sub: jwt.Permission{Allow: jwt.StringList{api, "$JS.FC.>"}},
	}
}
