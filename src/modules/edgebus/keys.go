package edgebus

import (
	"os"
	"path/filepath"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeKeys identifies a failure loading, creating, or using the hub's
// operator and account keys.
var ErrCodeKeys = errs.NewCode("edgebus/keys")

// hubKeys are the three key pairs an operator-mode hub needs: the operator
// that signs accounts, the system account the server runs its internals in,
// and the one tenant account every edge and central itself connect to. They
// are generated on first start into the state directory and read back
// afterwards, so a restart keeps every credential it minted valid.
type hubKeys struct {
	operator nkeys.KeyPair
	system   nkeys.KeyPair
	tenant   nkeys.KeyPair
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
	tenant, err := loadOrCreateKey(filepath.Join(keysDir, "tenant.nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	return &hubKeys{operator: operator, system: system, tenant: tenant}, nil
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
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			return nil, errs.From(err).Code(ErrCodeKeys).Attr("path", path).Msg("store key seed")
		}
		return pair, nil
	default:
		return nil, errs.From(err).Code(ErrCodeKeys).Attr("path", path).Msg("read key seed")
	}
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

// accountJWT signs one account's claims. JetStream is unlimited inside the
// account; the hub's own store limit bounds it.
func (k *hubKeys) accountJWT(account nkeys.KeyPair, name string, jetStream bool) (string, error) {
	pub, err := publicKey(account)
	if err != nil {
		return "", err
	}
	claims := jwt.NewAccountClaims(pub)
	claims.Name = name
	if jetStream {
		claims.Limits.JetStreamLimits = jwt.JetStreamLimits{
			MemoryStorage: jwt.NoLimit,
			DiskStorage:   jwt.NoLimit,
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

// EdgeCredentials is what AttachBus hands an edge: the tenant account's
// JWT, the user JWT the leaf authenticates with, and the seed that signs
// the server's nonce.
type EdgeCredentials struct {
	AccountJWT string
	UserJWT    string
	Seed       string
}

// CredsFile renders the user JWT and seed in the .creds layout a leaf
// remote reads.
func (c EdgeCredentials) CredsFile() ([]byte, error) {
	creds, err := jwt.FormatUserConfig(c.UserJWT, []byte(c.Seed))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeKeys).Msg("format user credentials")
	}
	return creds, nil
}

// mintUser signs a user in the tenant account with the given permissions.
func (k *hubKeys) mintUser(name string, permissions jwt.Permissions) (EdgeCredentials, error) {
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
	encoded, err := claims.Encode(k.tenant)
	if err != nil {
		return EdgeCredentials{}, errs.From(err).Code(ErrCodeKeys).Attr("user", name).Msg("encode user claims")
	}
	accountJWT, err := k.accountJWT(k.tenant, DefaultTenant, true)
	if err != nil {
		return EdgeCredentials{}, err
	}
	return EdgeCredentials{AccountJWT: accountJWT, UserJWT: encoded, Seed: string(seed)}, nil
}

// edgePermissions confines an edge's leaf to the traffic directions the
// fabric needs. Publish: its own subtree, its own JetStream API and inbox
// (so the hub can source its buffer), the hub's request inboxes, and the
// $JSC.R reply subjects the hub's own source client attaches to its
// consumer requests. Subscribe: that API and inbox, and the $JS.FC flow
// control replies the hub sends the sourcing consumer. Nothing else the hub
// publishes reaches the edge by this path.
func edgePermissions(tenant, edgeID string) jwt.Permissions {
	subtree := EdgeSubtree(tenant, edgeID) + ".>"
	api := "$JS." + EdgeDomain(edgeID) + ".API.>"
	inbox := "_INBOX." + edgeID + ".>"
	return jwt.Permissions{
		Pub: jwt.Permission{Allow: jwt.StringList{subtree, api, inbox, "_INBOX.>", "$JSC.R.>"}},
		Sub: jwt.Permission{Allow: jwt.StringList{api, inbox, "$JS.FC.>"}},
	}
}
