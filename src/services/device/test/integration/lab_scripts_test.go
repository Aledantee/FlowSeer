package integration_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// The lab scripts write beside themselves, so each test copies the script
// it runs into a temporary tree and starts it there. The scripts under test
// are named by the two variables below, which let one test body run the
// shell file and then the Python file that replaces it.
var (
	labSecretsScript = "write-lab-secrets.py"
	labStoreScript   = "write-openfga-store.py"
)

// labDir is where the scripts of a tree live, relative to the tree.
var labDir = filepath.Join("deploy", "lab")

// modelRelPath is where write-openfga-store reads the authorization model,
// relative to a tree.
var modelRelPath = filepath.Join("src", "services", "device", "internal", "authz", "openfga", "model.json")

// sharedSecrets is one generated secrets/ directory for the whole package.
// Generating it costs a 4096-bit RSA key, so tests that only read it share
// one. A t.TempDir() would end with the first test that asked for it.
var sharedSecrets struct {
	once sync.Once
	root string // a tree holding deploy/lab/secrets
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedSecrets.root != "" {
		_ = os.RemoveAll(sharedSecrets.root)
	}
	os.Exit(code)
}

// labRun is one run of a lab script.
type labRun struct {
	stdout string
	stderr string
	exit   int
}

// skipUnlessLabScriptsRun skips on a host that cannot start the scripts
// under test.
func skipUnlessLabScriptsRun(t *testing.T, names ...string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the lab scripts are run through POSIX paths and modes")
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".sh") {
			continue
		}
		for _, tool := range []string{"bash", "openssl", "htpasswd", "curl", "jq"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skipf("%s is not on PATH: %v", tool, err)
			}
		}
	}
}

// runLabScript copies the script into <tree>/deploy/lab, with the lock file
// beside it when it has one, and runs it there. A .sh file runs under the
// interpreter its first line names, and a .py file under `uv run`, with
// --locked when a lock file travels with it so that a lock that no longer
// matches the script's block fails the run.
func runLabScript(t *testing.T, tree, name string, args ...string) labRun {
	t.Helper()

	dir := filepath.Join(tree, labDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	copyFile(t, labFixturePath(name), filepath.Join(dir, name))

	var argv []string
	switch {
	case strings.HasSuffix(name, ".sh"):
		argv = []string{"bash", name}
	default:
		argv = []string{"uv", "run"}
		if _, err := os.Stat(labFixturePath(name + ".lock")); err == nil {
			copyFile(t, labFixturePath(name+".lock"), filepath.Join(dir, name+".lock"))
			argv = append(argv, "--locked")
		}
		argv = append(argv, name)
	}

	cmd := exec.Command(argv[0], append(argv[1:], args...)...)
	cmd.Dir = dir

	return startLabCommand(t, cmd, nil)
}

// startLabCommand runs cmd and returns its streams and exit status.
func startLabCommand(t *testing.T, cmd *exec.Cmd, env []string) labRun {
	t.Helper()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), env...)
	err := cmd.Run()

	run := labRun{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.exit = exit.ExitCode()
	default:
		t.Fatalf("running %v: %v\n%s", cmd.Args, err, run.stderr)
	}

	return run
}

// copyFile copies one file, keeping its content and nothing else.
func copyFile(t *testing.T, from, to string) {
	t.Helper()

	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("reading %s: %v", from, err)
	}
	if err := os.WriteFile(to, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", to, err)
	}
}

// copyDir copies the files of a flat directory.
func copyDir(t *testing.T, from, to string) {
	t.Helper()

	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatalf("creating %s: %v", to, err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatalf("listing %s: %v", from, err)
	}
	for _, entry := range entries {
		copyFile(t, filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name()))
	}
}

// generatedSecrets returns the package's shared secrets/ directory, which
// the secrets script wrote once. A test that changes it copies it first.
func generatedSecrets(t *testing.T) string {
	t.Helper()

	skipUnlessLabScriptsRun(t, labSecretsScript)

	sharedSecrets.once.Do(func() {
		root, err := os.MkdirTemp("", "lab-secrets-")
		if err != nil {
			sharedSecrets.err = err
			return
		}
		sharedSecrets.root = root
		run := runLabScript(t, root, labSecretsScript)
		if run.exit != 0 {
			sharedSecrets.err = fmt.Errorf("%s exited %d:\n%s", labSecretsScript, run.exit, run.stderr)
		}
	})
	if sharedSecrets.err != nil {
		t.Fatalf("generating the lab secrets: %v", sharedSecrets.err)
	}

	return filepath.Join(sharedSecrets.root, labDir, "secrets")
}

// labTree returns a new tree holding its own copy of the generated secrets
// and the authorization model the store script reads.
func labTree(t *testing.T) string {
	t.Helper()

	tree := t.TempDir()
	copyDir(t, generatedSecrets(t), filepath.Join(tree, labDir, "secrets"))
	modelPath := filepath.Join(tree, modelRelPath)
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o700); err != nil {
		t.Fatalf("creating the model directory: %v", err)
	}
	copyFile(t, filepath.Join("..", "..", "internal", "authz", "openfga", "model.json"), modelPath)

	return tree
}

// secretFile reads one file of a secrets directory.
func secretFile(t *testing.T, dir, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading secrets/%s: %v", name, err)
	}

	return string(body)
}

// pemBlock decodes the one PEM block of a file.
func pemBlock(t *testing.T, name, body string) *pem.Block {
	t.Helper()

	block, rest := pem.Decode([]byte(body))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("%s is not exactly one PEM block", name)
	}

	return block
}

// parseCert reads secrets/<name> as a certificate.
func parseCert(t *testing.T, dir, name string) *x509.Certificate {
	t.Helper()

	cert, err := x509.ParseCertificate(pemBlock(t, name, secretFile(t, dir, name)).Bytes)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}

	return cert
}

// rsaBits returns the modulus size of a certificate's key.
func rsaBits(t *testing.T, name string, cert *x509.Certificate) int {
	t.Helper()

	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("%s holds a %T key, want RSA", name, cert.PublicKey)
	}

	return key.N.BitLen()
}

func TestTheLabSecretsScriptWritesOwnerOnlyFiles(t *testing.T) {
	t.Parallel()

	dir := generatedSecrets(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}

	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("reading the mode of %s: %v", entry.Name(), err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("secrets/%s has mode %v, want -rw-------", entry.Name(), info.Mode().Perm())
		}
	}

	want := []string{
		"ca.crt", "ca.key", "credentials.txt", "dex.env", "dex_client.secret",
		"openfga.env", "openfga.key", "server.crt", "server.key",
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("secrets/ holds %v, want %v", got, want)
	}
}

// hashPrefix is what the script under test writes at the head of a bcrypt
// hash: htpasswd writes the $2y$ spelling and the bcrypt package $2b$.
func hashPrefix() string {
	if strings.HasSuffix(labSecretsScript, ".sh") {
		return `$2y$10$`
	}

	return `$2b$10$`
}

// dexHashes returns the user and bcrypt hash of each hash line of dex.env.
func dexHashes(t *testing.T, dir string) map[string]string {
	t.Helper()

	hashes := map[string]string{}
	line := regexp.MustCompile(`^DEX_USER_([A-Z]+)_HASH='(.*)'$`)
	for l := range strings.SplitSeq(secretFile(t, dir, "dex.env"), "\n") {
		if m := line.FindStringSubmatch(l); m != nil {
			hashes[strings.ToLower(m[1])] = m[2]
		}
	}
	if len(hashes) == 0 {
		t.Fatal("dex.env holds no hash line")
	}

	return hashes
}

// Dex's environment file keeps every value literal.
//
// Compose reads the file and substitutes $name in an unquoted value. A bcrypt
// hash is $2b$10$<salt><digest>, so unquoted it reaches Dex with its first
// segments eaten, and the password grant then fails with a login error that
// names nothing about the file. No test starts Compose, so this reads what
// the script wrote: every line is one single-quoted value, and each hash is
// whole and accepted for the password credentials.txt gives its user.
func TestTheLabSecretsScriptQuotesTheDexEnvFile(t *testing.T) {
	t.Parallel()

	dir := generatedSecrets(t)
	assignment := regexp.MustCompile(`^[A-Z_]+='[^']+'$`)
	for l := range strings.SplitSeq(strings.TrimSuffix(secretFile(t, dir, "dex.env"), "\n"), "\n") {
		if !assignment.MatchString(l) {
			t.Errorf("dex.env line %q is not one single-quoted value", l)
		}
	}

	shape := regexp.MustCompile(`^` + regexp.QuoteMeta(hashPrefix()) + `.{53}$`)
	for user, hash := range dexHashes(t, dir) {
		if !shape.MatchString(hash) {
			t.Errorf("hash of %s does not hold %s and 53 more characters", user, hashPrefix())
		}
		password := regexp.MustCompile(`(?m)^Dex User ` + user + `: (\S+)$`).FindStringSubmatch(secretFile(t, dir, "credentials.txt"))
		if password == nil {
			t.Fatalf("credentials.txt gives no password for %s", user)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password[1])); err != nil {
			t.Errorf("hash of %s does not accept the password in credentials.txt: %v", user, err)
		}
	}
}

// The lab password hashes meet the cost Dex requires.
//
// htpasswd defaults to cost 5 and Dex refuses anything below 10 at login, so a
// default invocation yields a file that loads and a user who cannot sign in.
func TestTheLabSecretsScriptHashesAtDexsCost(t *testing.T) {
	t.Parallel()

	for user, hash := range dexHashes(t, generatedSecrets(t)) {
		cost, err := bcrypt.Cost([]byte(hash))
		if err != nil {
			t.Errorf("reading the cost of the hash of %s: %v", user, err)
			continue
		}
		if cost != 10 {
			t.Errorf("hash of %s has cost %d, want 10", user, cost)
		}
	}
}

// importsSubprocess reports whether a Python file imports subprocess.
func importsSubprocess(body string) bool {
	return regexp.MustCompile(`(?m)^\s*(import|from)\s+subprocess\b`).MatchString(body)
}

// A password in a process's arguments is readable by every user of the host.
// A script that starts no process cannot put one there.
func TestTheLabSecretsScriptStartsNoProcess(t *testing.T) {
	t.Parallel()

	if strings.HasSuffix(labSecretsScript, ".sh") {
		t.Skip("a shell script starts the tools it uses")
	}
	if importsSubprocess(labScript(t, labSecretsScript)) {
		t.Errorf("%s imports subprocess", labSecretsScript)
	}
}

// The chain the script writes passes the checks a strict verifier makes.
//
// Python 3.13 sets VERIFY_X509_STRICT by default, and Go checks key usage and
// names, so a CA or server certificate that lacks an extension fails at the
// first TLS handshake and not at generation.
func TestTheLabSecretsScriptWritesAStrictChain(t *testing.T) {
	t.Parallel()

	if strings.HasSuffix(labSecretsScript, ".sh") {
		t.Skip("the shell file writes the chain openssl 3 defaults give")
	}

	dir := generatedSecrets(t)
	ca := parseCert(t, dir, "ca.crt")
	server := parseCert(t, dir, "server.crt")

	if !ca.IsCA || !ca.BasicConstraintsValid {
		t.Errorf("ca.crt: IsCA %v, BasicConstraintsValid %v, want both true", ca.IsCA, ca.BasicConstraintsValid)
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("ca.crt key usage %v lacks CertSign", ca.KeyUsage)
	}
	if bits := rsaBits(t, "ca.crt", ca); bits != 4096 {
		t.Errorf("ca.crt key is RSA %d, want 4096", bits)
	}
	if len(ca.SubjectKeyId) == 0 {
		t.Error("ca.crt has no subject key identifier")
	}

	if bits := rsaBits(t, "server.crt", server); bits != 2048 {
		t.Errorf("server.crt key is RSA %d, want 2048", bits)
	}
	if !slices.Equal(server.DNSNames, []string{"localhost"}) {
		t.Errorf("server.crt DNS names are %v, want [localhost]", server.DNSNames)
	}
	if len(server.IPAddresses) != 1 || !server.IPAddresses[0].Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("server.crt addresses are %v, want [127.0.0.1]", server.IPAddresses)
	}
	if !slices.Equal(server.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}) {
		t.Errorf("server.crt extended key usage is %v, want server and client auth", server.ExtKeyUsage)
	}
	if len(server.AuthorityKeyId) == 0 || !bytes.Equal(server.AuthorityKeyId, ca.SubjectKeyId) {
		t.Errorf("server.crt authority key id %x is not the CA's subject key id %x", server.AuthorityKeyId, ca.SubjectKeyId)
	}
	if lifetime := server.NotAfter.Sub(server.NotBefore); lifetime != 365*24*time.Hour {
		t.Errorf("server.crt lifetime is %v, want 365 days", lifetime)
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, name := range []string{"localhost", "127.0.0.1"} {
		opts := x509.VerifyOptions{Roots: pool, DNSName: name, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if _, err := server.Verify(opts); err != nil {
			t.Errorf("server.crt does not verify for %s: %v", name, err)
		}
	}

	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")); err != nil {
		t.Errorf("server.crt and server.key do not load as a pair: %v", err)
	}
}

// Every file the README reads from secrets/ is one the script writes.
//
// The README's run is not executed by any test, so a file name that drifts
// between the two fails only for whoever follows the steps.
func TestTheLabReadmeReadsOnlyFilesTheSecretsScriptWrites(t *testing.T) {
	t.Parallel()

	dir := generatedSecrets(t)
	named := regexp.MustCompile(`secrets/([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(labScript(t, "README.md"), -1)
	if len(named) == 0 {
		t.Fatal("deploy/lab/README.md names no file under secrets/")
	}
	for _, m := range named {
		if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
			t.Errorf("deploy/lab/README.md reads secrets/%s, which %s does not write", m[1], labSecretsScript)
		}
	}
}

// A second run leaves the first one's secrets alone.
//
// Overwriting would change the preshared key and the hashes under containers
// that already hold the old ones.
func TestTheLabSecretsScriptRefusesAnExistingDirectory(t *testing.T) {
	t.Parallel()

	tree := labTree(t)
	secrets := filepath.Join(tree, labDir, "secrets")
	before := dirContents(t, secrets)

	run := runLabScript(t, tree, labSecretsScript)
	if run.exit != 1 {
		t.Errorf("a second run exited %d, want 1:\n%s", run.exit, run.stderr)
	}
	if !strings.Contains(run.stderr, "already exists") {
		t.Errorf("a second run did not say why it refused:\n%s", run.stderr)
	}
	if after := dirContents(t, secrets); !maps.Equal(before, after) {
		t.Error("a refused run changed the secrets directory")
	}
}

// dirContents maps each file name of a flat directory to its content.
func dirContents(t *testing.T, dir string) map[string]string {
	t.Helper()

	got := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	for _, entry := range entries {
		got[entry.Name()] = secretFile(t, dir, entry.Name())
	}

	return got
}

// labOpenFGA is a TLS server that answers the two requests the store script
// makes, with the certificate the secrets script generated.
type labOpenFGA struct {
	server  *httptest.Server
	calls   atomic.Int32
	models  atomic.Pointer[string] // the body of the model request
	auth    atomic.Pointer[string] // the Authorization header of the last request
	storeID string                 // the answer to the create request; "" answers {}
}

// startLabOpenFGA serves the tree's server.crt and returns the server.
func startLabOpenFGA(t *testing.T, tree, storeID string) *labOpenFGA {
	t.Helper()

	secrets := filepath.Join(tree, labDir, "secrets")
	pair, err := tls.LoadX509KeyPair(filepath.Join(secrets, "server.crt"), filepath.Join(secrets, "server.key"))
	if err != nil {
		t.Fatalf("loading the generated certificate: %v", err)
	}

	f := &labOpenFGA{storeID: storeID}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		auth := r.Header.Get("Authorization")
		f.auth.Store(&auth)
		body, _ := io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/stores":
			if f.storeID == "" {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q}`, f.storeID)
		case r.Method == http.MethodPost && r.URL.Path == "/stores/"+f.storeID+"/authorization-models":
			model := string(body)
			f.models.Store(&model)
			_, _ = w.Write([]byte(`{"authorization_model_id":"M1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)

	return f
}

// runStoreScript runs the store script in the tree against the fake server.
func runStoreScript(t *testing.T, tree string, f *labOpenFGA) labRun {
	t.Helper()

	dir := filepath.Join(tree, labDir)
	name := labStoreScript
	copyFile(t, labFixturePath(name), filepath.Join(dir, name))

	argv := []string{"bash", name}
	if strings.HasSuffix(name, ".py") {
		argv = []string{"uv", "run", name}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir

	return startLabCommand(t, cmd, []string{"OPENFGA_HTTP_ENDPOINT=" + f.server.URL})
}

// The store script authenticates to a server the lab CA signed, with the
// preshared key, and prints the block an operator pastes into central's
// configuration.
//
// The script's TLS context keeps the verifier's strict defaults, so this
// fails if the generated chain lacks an extension Python asks for.
func TestTheLabStoreScriptPrintsTheAuthorizationBlock(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t, labSecretsScript, labStoreScript)
	tree := labTree(t)
	f := startLabOpenFGA(t, tree, "S1")

	run := runStoreScript(t, tree, f)
	if run.exit != 0 {
		t.Fatalf("the store script exited %d:\n%s", run.exit, run.stderr)
	}
	for _, want := range []string{`store_id: "S1"`, `model_id: "M1"`} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("the printed block lacks %s:\n%s", want, run.stdout)
		}
	}

	key := strings.TrimSpace(secretFile(t, filepath.Join(tree, labDir, "secrets"), "openfga.key"))
	if auth := f.auth.Load(); auth == nil || *auth != "Bearer "+key {
		t.Errorf("the last request carried Authorization %v, want Bearer <openfga.key>", auth)
	}
	want, err := os.ReadFile(filepath.Join(tree, modelRelPath))
	if err != nil {
		t.Fatalf("reading the model: %v", err)
	}
	if model := f.models.Load(); model == nil || *model != string(want) {
		t.Errorf("the model request body is not model.json")
	}
}

// otherCA builds a self-signed CA that no generated certificate chains to.
func otherCA(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Another CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a CA: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// A server the lab CA did not sign is not talked to.
//
// The preshared key goes in the first request, so a script that skipped the
// check would hand it to whoever answered.
func TestTheLabStoreScriptRefusesAnotherCa(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t, labSecretsScript, labStoreScript)
	tree := labTree(t)
	f := startLabOpenFGA(t, tree, "S1")
	caPath := filepath.Join(tree, labDir, "secrets", "ca.crt")
	if err := os.WriteFile(caPath, []byte(otherCA(t)), 0o600); err != nil {
		t.Fatalf("replacing ca.crt: %v", err)
	}

	run := runStoreScript(t, tree, f)
	if run.exit == 0 {
		t.Errorf("the store script accepted a server another CA signed:\n%s", run.stdout)
	}
	if strings.HasSuffix(labStoreScript, ".py") && run.exit != 1 {
		t.Errorf("the store script exited %d, want 1", run.exit)
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("the server's handler ran %d times, want 0", n)
	}
}

// A server that answers without an id is a failure.
func TestTheLabStoreScriptFailsWithoutAStoreId(t *testing.T) {
	t.Parallel()

	skipUnlessLabScriptsRun(t, labSecretsScript, labStoreScript)
	tree := labTree(t)
	f := startLabOpenFGA(t, tree, "")

	run := runStoreScript(t, tree, f)
	if run.exit != 1 {
		t.Errorf("the store script exited %d on an answer of {}, want 1", run.exit)
	}
	if !strings.Contains(run.stderr, "Failed to create OpenFGA store") {
		t.Errorf("the store script did not say what failed:\n%s", run.stderr)
	}
}

// The preshared key reaches the server as a header and nowhere else. A
// script that starts no process cannot put it in an argument list.
func TestTheLabStoreScriptStartsNoProcess(t *testing.T) {
	t.Parallel()

	if strings.HasSuffix(labStoreScript, ".sh") {
		t.Skip("a shell script starts the tools it uses")
	}
	if importsSubprocess(labScript(t, labStoreScript)) {
		t.Errorf("%s imports subprocess", labStoreScript)
	}
}
