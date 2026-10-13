// Package history is the ClickHouse side of the central history store: the
// connection pools the sink and the read call share, the migrator that owns
// the schema, and the device dimension mirror.
package history

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the package returns.
var (
	// ErrCodePassword is a password file that cannot be read or holds nothing.
	ErrCodePassword = errs.NewCode("history/password")
	// ErrCodeMigrate is a migration that stopped before every step applied.
	ErrCodeMigrate = errs.NewCode("history/migrate")
	// ErrCodeInsert is an insert the server refused or that never reached it.
	ErrCodeInsert = errs.NewCode("history/insert")
)

const (
	dialTimeout    = 5 * time.Second
	writerMaxOpen  = 4
	readerMaxOpen  = 16
	adminMaxOpen   = 1
	passwordMaxLen = 4096
)

// Store holds the writer, reader, and admin connection pools of one
// ClickHouse deployment. Opening a Store connects to nothing: a pool dials
// when it first serves a call, so a Store exists while the server is down. It
// is safe for concurrent use.
type Store struct {
	cfg    Config
	admin  clickhouse.Conn
	writer clickhouse.Conn
	reader clickhouse.Conn
}

// Open builds the pools for cfg. It returns an error only for a Config that
// names no usable deployment or a CA file that cannot be read, never for an
// unreachable server. Close releases the pools.
func Open(cfg Config) (*Store, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	tlsConfig, err := tlsFromCAFile(cfg.CAFile)
	if err != nil {
		return nil, err
	}

	s := &Store{cfg: cfg}
	pools := []struct {
		conn     *clickhouse.Conn
		user     User
		maxOpen  int
		settings clickhouse.Settings
	}{
		{&s.admin, cfg.Admin, adminMaxOpen, nil},
		{&s.writer, cfg.Writer, writerMaxOpen, clickhouse.Settings{"async_insert": 0}},
		{&s.reader, cfg.Reader, readerMaxOpen, nil},
	}
	for _, p := range pools {
		conn, err := clickhouse.Open(&clickhouse.Options{
			Addr:         cfg.Endpoints,
			Auth:         clickhouse.Auth{Username: p.user.Name},
			TLS:          tlsConfig,
			DialStrategy: dialStrategy(p.user),
			DialTimeout:  dialTimeout,
			MaxOpenConns: p.maxOpen,
			MaxIdleConns: p.maxOpen,
			Settings:     p.settings,
			Compression:  &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		})
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConfig).Attr("user", p.user.Name).Msg("build connection pool")
		}
		*p.conn = conn
	}
	return s, nil
}

// Writer returns the pool that inserts as the writer user. Its default
// settings turn asynchronous inserts off, so an insert returns after the
// server holds the rows.
func (s *Store) Writer() clickhouse.Conn { return s.writer }

// Reader returns the pool that reads as the reader user.
func (s *Store) Reader() clickhouse.Conn { return s.reader }

// Close closes every pool and returns the first error.
func (s *Store) Close() error {
	var first error
	for _, c := range []clickhouse.Conn{s.admin, s.writer, s.reader} {
		if err := c.Close(); err != nil && first == nil {
			first = errs.Wrap(err, "close connection pool")
		}
	}
	return first
}

// dialStrategy reads the user's password file for every new connection and
// tries the endpoints in order. clickhouse-go keeps the password in its
// options, so a strategy that substitutes it per dial is the only way to
// honor a rotated file without reopening the pool.
func dialStrategy(user User) func(ctx context.Context, connID int, opt *clickhouse.Options, dial clickhouse.Dial) (clickhouse.DialResult, error) {
	return func(ctx context.Context, _ int, opt *clickhouse.Options, dial clickhouse.Dial) (clickhouse.DialResult, error) {
		password, err := readPassword(user)
		if err != nil {
			return clickhouse.DialResult{}, err
		}
		o := *opt
		o.Auth.Password = password

		var last error
		for _, addr := range opt.Addr {
			result, err := dial(ctx, addr, &o)
			if err == nil {
				return result, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return clickhouse.DialResult{}, last
	}
}

// readPassword returns the file's contents without one trailing line break.
func readPassword(user User) (string, error) {
	data, err := os.ReadFile(user.PasswordFile)
	if err != nil {
		return "", errs.From(err).Code(ErrCodePassword).Attr("user", user.Name).Msg("read password file")
	}
	data = bytes.TrimSuffix(bytes.TrimSuffix(data, []byte("\n")), []byte("\r"))
	if len(data) == 0 || len(data) > passwordMaxLen {
		return "", errs.New().Code(ErrCodePassword).Attr("user", user.Name).Attr("length", len(data)).Msg("password file is empty or too long")
	}
	return string(data), nil
}

func tlsFromCAFile(path string) (*tls.Config, error) {
	if path == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeConfig).Msg("read CA file")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errs.New().Code(ErrCodeConfig).Msg("CA file holds no certificate")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}
