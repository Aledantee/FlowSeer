package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/ClickHouse/clickhouse-go/v2"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeNewerSchema is a server whose schema_steps holds a step this build
// does not know, so a newer binary migrated it.
var ErrCodeNewerSchema = errs.NewCode("history/newer-schema")

//go:embed sql
var sqlFiles embed.FS

// statementMarker separates statements inside one SQL file. ClickHouse takes
// one statement per query, and a semicolon can occur inside a string.
const statementMarker = "-- statement"

// AppliedStep is one migration step a [Migrate] run applied.
type AppliedStep struct {
	Number int
	Name   string
}

type step struct {
	number     int
	name       string
	statements []string
}

// executor is the part of a ClickHouse connection the migrator needs, so a
// test can stand in for the server.
type executor interface {
	exec(ctx context.Context, statement string) error
	applied(ctx context.Context) ([]int, error)
	record(ctx context.Context, number int, name string) error
}

// Migrate brings the server to the schema this build embeds. It connects as
// the admin user. It creates the databases, reconciles the writer, reader, and
// analyst users with their password files, then applies each embedded step
// that schema_steps does not record, in order, and records it. A second run
// applies nothing.
//
// It stops at the first failing step and leaves it unrecorded, so the next run
// starts there: every statement of a step is idempotent. It returns
// [ErrCodeNewerSchema] when schema_steps holds a step number above the last
// step this build embeds, since an older binary must not touch a newer schema.
//
// Run Migrate from one process at a time. schema_steps holds no lock.
func Migrate(ctx context.Context, s *Store) ([]AppliedStep, error) {
	data := newTemplateData(s.cfg)
	bootstrap, steps, err := loadSteps(sqlFiles)
	if err != nil {
		return nil, err
	}
	users, err := userStatements(data, s.cfg)
	if err != nil {
		return nil, err
	}
	return migrate(ctx, newExecutor(s.admin, data), data, bootstrap, users, steps)
}

func migrate(ctx context.Context, ex executor, data templateData, bootstrap, users []string, steps []step) ([]AppliedStep, error) {
	for _, tmpl := range bootstrap {
		stmt, err := render(tmpl, data)
		if err != nil {
			return nil, err
		}
		if err := ex.exec(ctx, stmt); err != nil {
			return nil, errs.From(err).Code(ErrCodeMigrate).Msg("apply bootstrap statement")
		}
	}
	for _, stmt := range users {
		if err := ex.exec(ctx, stmt); err != nil {
			return nil, errs.From(err).Code(ErrCodeMigrate).Msg("reconcile users")
		}
	}

	recorded, err := ex.applied(ctx)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeMigrate).Msg("read schema_steps")
	}
	newest := 0
	for _, n := range recorded {
		newest = max(newest, n)
	}
	if newest > len(steps) {
		return nil, errs.New().Code(ErrCodeNewerSchema).Attr("recorded", newest).Attr("known", len(steps)).
			Msg("the server schema is newer than this build")
	}

	var done []AppliedStep
	for _, st := range steps {
		if slices.Contains(recorded, st.number) {
			continue
		}
		for _, tmpl := range st.statements {
			stmt, err := render(tmpl, data)
			if err != nil {
				return done, errs.From(err).Attr("step", st.number).Attr("name", st.name).Msg("render step")
			}
			if err := ex.exec(ctx, stmt); err != nil {
				return done, errs.From(err).Code(ErrCodeMigrate).Attr("step", st.number).Attr("name", st.name).Msg("apply step")
			}
		}
		if err := ex.record(ctx, st.number, st.name); err != nil {
			return done, errs.From(err).Code(ErrCodeMigrate).Attr("step", st.number).Attr("name", st.name).Msg("record step")
		}
		done = append(done, AppliedStep{Number: st.number, Name: st.name})
	}
	return done, nil
}

// loadSteps reads sql/bootstrap.sql and the numbered files under sql/steps.
// The numbers must run 1, 2, 3 without a gap, so the count of steps is also
// the newest step number.
func loadSteps(fsys fs.FS) (bootstrap []string, steps []step, err error) {
	raw, err := fs.ReadFile(fsys, "sql/bootstrap.sql")
	if err != nil {
		return nil, nil, errs.Wrap(err, "read bootstrap")
	}
	bootstrap = splitStatements(string(raw))

	entries, err := fs.ReadDir(fsys, "sql/steps")
	if err != nil {
		return nil, nil, errs.Wrap(err, "read steps")
	}
	for i, entry := range entries {
		base := strings.TrimSuffix(entry.Name(), ".sql")
		numStr, name, ok := strings.Cut(base, "_")
		number, convErr := strconv.Atoi(numStr)
		if !ok || convErr != nil || number != i+1 {
			return nil, nil, errs.New().Attr("file", entry.Name()).Attr("want", i+1).Msg("step files must be named NNNN_name.sql and numbered from 1 without a gap")
		}
		body, err := fs.ReadFile(fsys, path.Join("sql/steps", entry.Name()))
		if err != nil {
			return nil, nil, errs.Wrap(err, "read step")
		}
		steps = append(steps, step{number: number, name: name, statements: splitStatements(string(body))})
	}
	return bootstrap, steps, nil
}

// splitStatements cuts a file at its marker lines and drops pieces that hold
// only comments.
func splitStatements(src string) []string {
	var out []string
	var cur []string
	flush := func() {
		if text := strings.TrimSpace(strings.Join(cur, "\n")); hasCode(text) {
			out = append(out, text)
		}
		cur = nil
	}
	for line := range strings.SplitSeq(src, "\n") {
		if strings.TrimSpace(line) == statementMarker {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

func hasCode(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
			return true
		}
	}
	return false
}

// templateData is what a SQL file's template reads. Names arrive quoted.
type templateData struct {
	DB        string
	Control   string
	Writer    string
	Reader    string
	Analyst   string
	OnCluster string
	cluster   bool
}

func newTemplateData(cfg Config) templateData {
	d := templateData{
		DB:      quote(cfg.Database),
		Control: quote(cfg.Database + "_control"),
		Writer:  quote(cfg.Writer.Name),
		Reader:  quote(cfg.Reader.Name),
		Analyst: quote(cfg.Analyst.Name),
		cluster: cfg.Cluster != "",
	}
	if d.cluster {
		d.OnCluster = "ON CLUSTER " + quote(cfg.Cluster)
	}
	return d
}

// DatabaseEngine is the ENGINE clause of a database: Replicated on a cluster,
// the server default otherwise.
func (d templateData) DatabaseEngine() string {
	if d.cluster {
		return "ENGINE = Replicated"
	}
	return ""
}

// Engine is the ENGINE clause of a MergeTree-family table. On a cluster the
// engine is the Replicated variant with no Keeper path, which a Replicated
// database fills from the server defaults.
func (d templateData) Engine(family string, args ...string) string {
	name := family
	if d.cluster {
		name = "Replicated" + family
	}
	if len(args) == 0 {
		return "ENGINE = " + name
	}
	return "ENGINE = " + name + "(" + strings.Join(args, ", ") + ")"
}

func quote(identifier string) string { return "`" + identifier + "`" }

func render(tmpl string, data templateData) (string, error) {
	t, err := template.New("sql").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", errs.Wrap(err, "parse SQL template")
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", errs.Wrap(err, "render SQL template")
	}
	return buf.String(), nil
}

// userStatements creates or updates the writer, reader, and analyst users from
// their password files. The statement carries the SHA-256 of the password, so
// the plaintext never travels in SQL text, and ALTER makes a rotated file take
// effect on the next run.
func userStatements(data templateData, cfg Config) ([]string, error) {
	var out []string
	for _, u := range []User{cfg.Writer, cfg.Reader, cfg.Analyst} {
		password, err := readPassword(u)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(password))
		auth := "IDENTIFIED WITH sha256_hash BY '" + strings.ToUpper(hex.EncodeToString(sum[:])) + "'"
		name := quote(u.Name)
		out = append(out,
			"CREATE USER IF NOT EXISTS "+name+" "+data.OnCluster+" "+auth,
			"ALTER USER "+name+" "+data.OnCluster+" "+auth,
		)
	}
	return out, nil
}

type clickHouseExecutor struct {
	conn    clickhouse.Conn
	control string
}

func newExecutor(conn clickhouse.Conn, data templateData) *clickHouseExecutor {
	return &clickHouseExecutor{conn: conn, control: data.Control}
}

// recordContext makes a step record synchronous. The server acknowledges it
// after one replica holds it, so a second run through another replica that
// lags may run a step again. Every statement of a step is idempotent and
// schema_steps collapses the repeated record on step.
func (e *clickHouseExecutor) recordContext(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"async_insert": 0}))
}

func (e *clickHouseExecutor) exec(ctx context.Context, statement string) error {
	return e.conn.Exec(ctx, statement)
}

func (e *clickHouseExecutor) applied(ctx context.Context) ([]int, error) {
	rows, err := e.conn.Query(e.recordContext(ctx), "SELECT step FROM "+e.control+".schema_steps FINAL ORDER BY step")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []int
	for rows.Next() {
		var n uint32
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, int(n))
	}
	return out, rows.Err()
}

func (e *clickHouseExecutor) record(ctx context.Context, number int, name string) error {
	return e.conn.Exec(e.recordContext(ctx), "INSERT INTO "+e.control+".schema_steps (step, name) VALUES (?, ?)", uint32(number), name)
}
