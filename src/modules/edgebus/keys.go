package edgebus

import (
	"os"
	"path/filepath"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// ErrCodeKeys identifies a failure loading, creating, or using the hub's
// operator and account keys.
var ErrCodeKeys = errs.NewCode("edgebus/keys")

// hubKeys holds the operator, system, and CENTRAL keys persisted across starts.
type hubKeys struct {
	operator nkeys.KeyPair
	system   nkeys.KeyPair
	central  nkeys.KeyPair
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
	return &hubKeys{operator: operator, system: system, central: central}, nil
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
		// keep the hub from starting.
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
// account a JetStream disk budget. Zero leaves JetStream off.
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

type credentials struct {
	AccountJWT string
	UserJWT    secret.Value
	Seed       secret.Value
}

// mintUser signs a user in account with the given permissions.
func (k *hubKeys) mintUser(account nkeys.KeyPair, accountJWT, name string, permissions jwt.Permissions) (credentials, error) {
	user, err := nkeys.CreateUser()
	if err != nil {
		return credentials{}, errs.From(err).Code(ErrCodeKeys).Msg("create user key")
	}
	userPub, err := publicKey(user)
	if err != nil {
		return credentials{}, err
	}
	seed, err := user.Seed()
	if err != nil {
		return credentials{}, errs.From(err).Code(ErrCodeKeys).Msg("read user seed")
	}
	claims := jwt.NewUserClaims(userPub)
	claims.Name = name
	claims.Permissions = permissions
	encoded, err := claims.Encode(account)
	if err != nil {
		return credentials{}, errs.From(err).Code(ErrCodeKeys).Attr("user", name).Msg("encode user claims")
	}
	return credentials{AccountJWT: accountJWT, UserJWT: secret.NewString(encoded), Seed: secret.New(seed)}, nil
}
