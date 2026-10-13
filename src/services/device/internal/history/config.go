package history

import (
	"net"
	"regexp"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeConfig is a Config that cannot name a ClickHouse deployment.
var ErrCodeConfig = errs.NewCode("history/config")

// identifier is the form of every database, cluster, and user name. The
// migrator interpolates these names into DDL, so a name outside it never
// reaches a statement.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Config names the ClickHouse deployment and the four users the store
// connects as. The zero value is invalid.
type Config struct {
	// Endpoints are the native-protocol addresses as host:port, in preference
	// order. A connection tries them in order and takes the first that answers.
	Endpoints []string
	// Database holds every history table. The migrator also creates a control
	// database beside it, named Database + "_control".
	Database string
	// Cluster is the server's cluster name from remote_servers. Empty means a
	// single node: the migrator creates plain engines and runs no statement
	// ON CLUSTER. Set, it creates a Replicated database with Replicated
	// engines and runs every user, profile, grant, row policy, resource, and
	// workload ON CLUSTER.
	Cluster string
	// Admin creates schema. Only [Migrate] connects as it.
	Admin User
	// Writer inserts history rows.
	Writer User
	// Reader serves the history read.
	Reader User
	// Analyst is the user fleet analytics connects as. The store never
	// connects as it, and the migrator creates it.
	Analyst User
	// CAFile is an absolute path to the PEM bundle that verifies the server
	// certificate. Empty connects without TLS.
	CAFile string
}

// User is one ClickHouse user and the file that holds its password. The file
// is read each time a connection opens, so a rotated secret needs no restart.
type User struct {
	Name         string
	PasswordFile string
}

func (c Config) validate() error {
	if len(c.Endpoints) == 0 {
		return errs.New().Code(ErrCodeConfig).Msg("no endpoints")
	}
	for _, e := range c.Endpoints {
		if _, port, err := net.SplitHostPort(e); err != nil || port == "" {
			return errs.New().Code(ErrCodeConfig).Attr("endpoint", e).Msg("endpoint is not host:port")
		}
	}
	if !identifier.MatchString(c.Database) {
		return errs.New().Code(ErrCodeConfig).Attr("database", c.Database).Msg("invalid database name")
	}
	if c.Cluster != "" && !identifier.MatchString(c.Cluster) {
		return errs.New().Code(ErrCodeConfig).Attr("cluster", c.Cluster).Msg("invalid cluster name")
	}
	roles := []struct {
		role string
		user User
	}{{"admin", c.Admin}, {"writer", c.Writer}, {"reader", c.Reader}, {"analyst", c.Analyst}}
	for _, r := range roles {
		if !identifier.MatchString(r.user.Name) {
			return errs.New().Code(ErrCodeConfig).Attr("role", r.role).Attr("user", r.user.Name).Msg("invalid user name")
		}
		if r.user.PasswordFile == "" {
			return errs.New().Code(ErrCodeConfig).Attr("role", r.role).Msg("no password file")
		}
	}
	return nil
}
