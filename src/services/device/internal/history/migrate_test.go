package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// fakeExecutor records what the migrator sends and answers from a script.
type fakeExecutor struct {
	statements    []string
	recorded      []int
	recordedSteps []int
	failOn        string
}

func (f *fakeExecutor) exec(_ context.Context, statement string) error {
	f.statements = append(f.statements, statement)
	if f.failOn != "" && strings.Contains(statement, f.failOn) {
		return errors.New("server refused the statement")
	}
	return nil
}

func (f *fakeExecutor) applied(context.Context) ([]int, error) { return f.recordedSteps, nil }

func (f *fakeExecutor) record(_ context.Context, number int, _ string) error {
	f.recorded = append(f.recorded, number)
	return nil
}

var testSteps = []step{
	{number: 1, name: "first", statements: []string{"CREATE TABLE one", "CREATE TABLE one_view"}},
	{number: 2, name: "second", statements: []string{"CREATE TABLE two"}},
	{number: 3, name: "third", statements: []string{"CREATE TABLE three"}},
}

func testData() templateData {
	return newTemplateData(Config{Database: "flowseer", Writer: User{Name: "w"}, Reader: User{Name: "r"}, Analyst: User{Name: "a"}})
}

func TestMigrateAppliesStepsInOrderAndRecordsEach(t *testing.T) {
	ex := &fakeExecutor{}

	done, err := migrate(context.Background(), ex, testData(), []string{"CREATE DATABASE d"}, []string{"CREATE USER u"}, testSteps)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []string{"CREATE DATABASE d", "CREATE USER u", "CREATE TABLE one", "CREATE TABLE one_view", "CREATE TABLE two", "CREATE TABLE three"}
	if !reflect.DeepEqual(ex.statements, want) {
		t.Errorf("statements = %q, want %q", ex.statements, want)
	}
	if !reflect.DeepEqual(ex.recorded, []int{1, 2, 3}) {
		t.Errorf("recorded = %v, want [1 2 3]", ex.recorded)
	}
	if len(done) != 3 || done[0].Name != "first" || done[2].Number != 3 {
		t.Errorf("applied = %v, want the three steps", done)
	}
}

func TestMigrateSkipsRecordedStepsButStillRunsBootstrapAndUsers(t *testing.T) {
	ex := &fakeExecutor{recordedSteps: []int{1, 2}}

	done, err := migrate(context.Background(), ex, testData(), []string{"CREATE DATABASE d"}, []string{"ALTER USER u"}, testSteps)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []string{"CREATE DATABASE d", "ALTER USER u", "CREATE TABLE three"}
	if !reflect.DeepEqual(ex.statements, want) {
		t.Errorf("statements = %q, want %q", ex.statements, want)
	}
	if len(done) != 1 || done[0].Number != 3 {
		t.Errorf("applied = %v, want only step 3", done)
	}
}

func TestMigrateRefusesASchemaNewerThanTheBuild(t *testing.T) {
	ex := &fakeExecutor{recordedSteps: []int{1, 2, 3, 4}}

	_, err := migrate(context.Background(), ex, testData(), []string{"CREATE DATABASE d"}, nil, testSteps)

	if code, _ := errs.CodeOf(err); code != ErrCodeNewerSchema {
		t.Fatalf("code = %v (err %v), want %v", code, err, ErrCodeNewerSchema)
	}
	if len(ex.recorded) != 0 || len(ex.statements) != 1 {
		t.Errorf("a newer schema ran %q and recorded %v, want only the bootstrap statement", ex.statements, ex.recorded)
	}
}

func TestMigrateStopsAtAFailedStepAndLeavesItUnrecorded(t *testing.T) {
	ex := &fakeExecutor{failOn: "CREATE TABLE two"}

	done, err := migrate(context.Background(), ex, testData(), nil, nil, testSteps)

	if code, _ := errs.CodeOf(err); code != ErrCodeMigrate {
		t.Fatalf("code = %v (err %v), want %v", code, err, ErrCodeMigrate)
	}
	if !reflect.DeepEqual(ex.recorded, []int{1}) {
		t.Errorf("recorded = %v, want only step 1", ex.recorded)
	}
	if len(done) != 1 {
		t.Errorf("applied = %v, want only step 1", done)
	}
	for _, stmt := range ex.statements {
		if strings.Contains(stmt, "three") {
			t.Errorf("step 3 ran after step 2 failed: %q", ex.statements)
		}
	}
}

func TestLoadStepsReadsTheEmbeddedFilesInOrder(t *testing.T) {
	bootstrap, steps, err := loadSteps(sqlFiles)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(bootstrap) != 3 {
		t.Errorf("bootstrap holds %d statements, want 3", len(bootstrap))
	}
	if len(steps) != 7 {
		t.Fatalf("steps = %d, want 7", len(steps))
	}
	for i, st := range steps {
		if st.number != i+1 || len(st.statements) == 0 {
			t.Errorf("step %d = %+v, want number %d and statements", i, st, i+1)
		}
	}
}

func TestLoadStepsRejectsAGapInTheNumbering(t *testing.T) {
	fsys := fstest.MapFS{
		"sql/bootstrap.sql":    {Data: []byte("SELECT 1")},
		"sql/steps/0001_a.sql": {Data: []byte("SELECT 1")},
		"sql/steps/0003_c.sql": {Data: []byte("SELECT 3")},
	}

	if _, _, err := loadSteps(fsys); err == nil {
		t.Fatal("a gap between step 1 and step 3 loaded, want an error")
	}
}

func TestSplitStatementsCutsAtMarkersAndDropsCommentOnlyPieces(t *testing.T) {
	src := "-- leading note\nCREATE A\n  (x UInt8)\n-- statement\n-- only a note\n-- statement\nCREATE B; -- keeps a semicolon in a string 'a;b'\n"

	got := splitStatements(src)

	want := []string{"-- leading note\nCREATE A\n  (x UInt8)", "CREATE B; -- keeps a semicolon in a string 'a;b'"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statements = %q, want %q", got, want)
	}
}

func renderAll(t *testing.T, cfg Config) []string {
	t.Helper()
	data := newTemplateData(cfg)
	bootstrap, steps, err := loadSteps(sqlFiles)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out []string
	templates := append([]string{}, bootstrap...)
	for _, st := range steps {
		templates = append(templates, st.statements...)
	}
	for _, tmpl := range templates {
		stmt, err := render(tmpl, data)
		if err != nil {
			t.Fatalf("render %q: %v", tmpl, err)
		}
		out = append(out, stmt)
	}
	return out
}

func writePassword(t *testing.T, dir, name, password string) User {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	return User{Name: name, PasswordFile: path}
}

func testConfig(t *testing.T, cluster string) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Endpoints: []string{"127.0.0.1:9000"},
		Database:  "flowseer",
		Cluster:   cluster,
		Admin:     writePassword(t, dir, "admin", "admin-secret"),
		Writer:    writePassword(t, dir, "flowseer_writer", "writer-secret"),
		Reader:    writePassword(t, dir, "flowseer_reader", "reader-secret"),
		Analyst:   writePassword(t, dir, "flowseer_analyst", "analyst-secret"),
	}
}

// accessEntity matches the statements whose objects belong to the server and
// not to a Replicated database, so they must say ON CLUSTER themselves.
var accessEntity = []string{"CREATE USER", "ALTER USER", "CREATE SETTINGS PROFILE", "CREATE ROW POLICY", "CREATE RESOURCE", "CREATE WORKLOAD", "GRANT"}

func isAccessEntity(stmt string) bool {
	for _, prefix := range accessEntity {
		if strings.Contains(stmt, prefix+" ") {
			return true
		}
	}
	return false
}

func TestOnAClusterEveryAccessEntityRunsOnTheClusterAndEveryTableIsReplicated(t *testing.T) {
	cfg := testConfig(t, "flowseer")
	users, err := userStatements(newTemplateData(cfg), cfg)
	if err != nil {
		t.Fatalf("users: %v", err)
	}

	for _, stmt := range append(renderAll(t, cfg), users...) {
		if isAccessEntity(stmt) && !strings.Contains(stmt, "ON CLUSTER `flowseer`") {
			t.Errorf("access statement without ON CLUSTER:\n%s", stmt)
		}
		if strings.Contains(stmt, "CREATE TABLE") && strings.Contains(stmt, "ON CLUSTER") {
			t.Errorf("a table inside a Replicated database must not run ON CLUSTER:\n%s", stmt)
		}
		if strings.Contains(stmt, "CREATE TABLE") && !strings.Contains(stmt, "ENGINE = Replicated") {
			t.Errorf("table without a Replicated engine on a cluster:\n%s", stmt)
		}
		if strings.Contains(stmt, "CREATE DATABASE") && !strings.Contains(stmt, "ENGINE = Replicated") {
			t.Errorf("database without the Replicated engine on a cluster:\n%s", stmt)
		}
	}
}

func TestOnASingleNodeNoStatementRunsOnAClusterOrUsesAReplicatedEngine(t *testing.T) {
	cfg := testConfig(t, "")
	users, err := userStatements(newTemplateData(cfg), cfg)
	if err != nil {
		t.Fatalf("users: %v", err)
	}

	for _, stmt := range append(renderAll(t, cfg), users...) {
		if strings.Contains(stmt, "ON CLUSTER") || strings.Contains(stmt, "Replicated") {
			t.Errorf("single-node statement names a cluster or a replicated engine:\n%s", stmt)
		}
	}
}

func TestEveryEmbeddedStatementIsIdempotent(t *testing.T) {
	for _, stmt := range renderAll(t, testConfig(t, "flowseer")) {
		upper := strings.ToUpper(stmt)
		creates := strings.Contains(upper, "CREATE ")
		if creates && !strings.Contains(upper, "IF NOT EXISTS") && !strings.Contains(upper, "OR REPLACE") {
			t.Errorf("a CREATE statement a rerun would fail on:\n%s", stmt)
		}
	}
}

func TestUserStatementsCarryTheSHA256OfThePasswordNotThePassword(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, "")
	cfg.Writer = writePassword(t, dir, "w", "password")
	cfg.Reader = writePassword(t, dir, "r", "other")
	cfg.Analyst = writePassword(t, dir, "a", "third")

	got, err := userStatements(newTemplateData(cfg), cfg)
	if err != nil {
		t.Fatalf("users: %v", err)
	}

	// SHA-256("password") is a published constant.
	const sha256Password = "5E884898DA28047151D0E56F8DC6292773603D0D6AABBDD62A11EF721D1542D8"
	want := "CREATE USER IF NOT EXISTS `w`  IDENTIFIED WITH sha256_hash BY '" + sha256Password + "'"
	if len(got) != 6 || got[0] != want {
		t.Fatalf("statements = %q, want first %q of 6", got, want)
	}
	for _, stmt := range got {
		for _, secret := range []string{"password", "other", "third"} {
			if strings.Contains(stmt, "'"+secret+"'") {
				t.Errorf("statement carries a plaintext password:\n%s", stmt)
			}
		}
	}
	if !strings.HasPrefix(got[1], "ALTER USER `w`") {
		t.Errorf("second statement = %q, want the ALTER of the same user", got[1])
	}
}
