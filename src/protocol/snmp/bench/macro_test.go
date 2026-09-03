//go:build snmp_bench_macro

package bench

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	g "github.com/gosnmp/gosnmp"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// macro_test.go — Tier 2 macro benchmarks. Realistic wall-clock workloads
// against a LIVE SNMP agent, comparing the FlowSeer client, the gosnmp
// client, and (via hyperfine) the Net-SNMP CLI tools.
//
// Gated two ways so it never runs in normal CI:
//   - build tag snmp_bench_macro
//   - env var FLOWSEER_BENCH_AGENT (host:port of a reachable agent)
//
// Run:
//
//	FLOWSEER_BENCH_AGENT=10.20.0.1:161 \
//	  go test -tags snmp_bench_macro -bench Macro -benchmem -run TestMacro -v .
//
// Env knobs:
//
//	FLOWSEER_BENCH_AGENT       host:port (required)
//	FLOWSEER_BENCH_COMMUNITY   v2c community (default "public")
//	FLOWSEER_BENCH_WALK_ROOT   dotted OID to walk (default ifTable 1.3.6.1.2.1.2.2)
//	FLOWSEER_BENCH_V3_USER     enables the v3/USM cold-start benchmark
//	FLOWSEER_BENCH_V3_AUTHPROTO  SHA|SHA256|MD5      (default SHA)
//	FLOWSEER_BENCH_V3_AUTHPASS
//	FLOWSEER_BENCH_V3_PRIVPROTO  AES|DES|none        (default none)
//	FLOWSEER_BENCH_V3_PRIVPASS

const (
	defaultWalkRoot  = "1.3.6.1.2.1.2.2"   // ifTable
	defaultScalarOID = "1.3.6.1.2.1.1.1.0" // sysDescr.0
)

type macroCfg struct {
	agent     string
	community string
	walkRoot  string
}

// macroEnv reads the macro config or skips the benchmark when the agent
// env var is unset.
func macroEnv(tb testing.TB) macroCfg {
	tb.Helper()
	agent := os.Getenv("FLOWSEER_BENCH_AGENT")
	if agent == "" {
		tb.Skip("FLOWSEER_BENCH_AGENT unset; macro tier needs a live agent")
	}
	return macroCfg{
		agent:     agent,
		community: getenvOr("FLOWSEER_BENCH_COMMUNITY", "public"),
		walkRoot:  getenvOr("FLOWSEER_BENCH_WALK_ROOT", defaultWalkRoot),
	}
}

func dialNativeV2c(tb testing.TB, c macroCfg) snmp.Session {
	tb.Helper()
	sess, err := snmp.NewSession(context.Background(), c.agent, snmp.V2c,
		snmp.WithCommunity(c.community),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(3*time.Second),
		snmp.WithRetries(2),
	)
	if err != nil {
		tb.Fatalf("native dial: %v", err)
	}
	return sess
}

func dialGosnmpV2c(tb testing.TB, c macroCfg) *g.GoSNMP {
	tb.Helper()
	host, port := gosnmpHostPort(tb, c.agent)
	client := &g.GoSNMP{
		Target:    host,
		Port:      port,
		Community: c.community,
		Version:   g.Version2c,
		Timeout:   3 * time.Second,
		Retries:   2,
	}
	if err := client.Connect(); err != nil {
		tb.Fatalf("gosnmp connect: %v", err)
	}
	return client
}

func walkNative(tb testing.TB, sess snmp.Session, root snmp.OID) {
	tb.Helper()
	w := sess.BulkWalk(context.Background(), root)
	for range w.Iter() {
	}
	if err := w.Err(); err != nil {
		tb.Fatalf("native BulkWalk: %v", err)
	}
}

// mustRoot parses the configured walk root once, so the dotted-string
// parse stays out of the timed benchmark loop.
func mustRoot(tb testing.TB, c macroCfg) snmp.OID {
	tb.Helper()
	root, err := snmp.ParseOID(c.walkRoot)
	if err != nil {
		tb.Fatalf("parse walk root %q: %v", c.walkRoot, err)
	}
	return root
}

func walkGosnmp(tb testing.TB, client *g.GoSNMP, root string) {
	tb.Helper()
	_, err := client.BulkWalkAll(root)
	if err != nil {
		tb.Fatalf("gosnmp BulkWalkAll: %v", err)
	}
}

// BenchmarkMacroWalk — steady-state full-table walk against the live agent.
func BenchmarkMacroWalk(b *testing.B) {
	c := macroEnv(b)

	b.Run("impl=flowseer", func(b *testing.B) {
		root := mustRoot(b, c)
		sess := dialNativeV2c(b, c)
		closeOnCleanup(b, sess)
		walkNative(b, sess, root) // warmup
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			walkNative(b, sess, root)
		}
	})

	b.Run("impl=gosnmp", func(b *testing.B) {
		client := dialGosnmpV2c(b, c)
		closeOnCleanup(b, client.Conn)
		walkGosnmp(b, client, c.walkRoot) // warmup
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			walkGosnmp(b, client, c.walkRoot)
		}
	})
}

// BenchmarkMacroColdStartV2c — construct + first walk per iteration (no
// warmup). Captures session construction + first round-trip over the real
// network. v2c has no engine discovery; see the v3 benchmark for that.
func BenchmarkMacroColdStartV2c(b *testing.B) {
	c := macroEnv(b)

	b.Run("impl=flowseer", func(b *testing.B) {
		root := mustRoot(b, c)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sess := dialNativeV2c(b, c)
			walkNative(b, sess, root)
			_ = sess.Close()
		}
	})

	b.Run("impl=gosnmp", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			client := dialGosnmpV2c(b, c)
			walkGosnmp(b, client, c.walkRoot)
			_ = client.Conn.Close()
		}
	})
}

// BenchmarkMacroColdStartV3USM — the cold-start that matters: each
// iteration builds a fresh v3 session and performs the first Get, which
// triggers the USM engine-discovery handshake (and time sync). This is
// the discovery cost the micro tier cannot show. Skips unless v3 creds are
// provided.
func BenchmarkMacroColdStartV3USM(b *testing.B) {
	c := macroEnv(b)
	usm, ok := v3USMFromEnv(b)
	if !ok {
		b.Skip("FLOWSEER_BENCH_V3_USER unset; skipping v3/USM cold-start")
	}
	scalar, err := snmp.ParseOID(defaultScalarOID)
	if err != nil {
		b.Fatalf("parse scalar: %v", err)
	}
	ctx := context.Background()

	b.Run("impl=flowseer", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sess, err := snmp.NewSession(ctx, c.agent, snmp.V3,
				snmp.WithUSM(usm.USMConfig),
				snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
				snmp.WithTimeout(3*time.Second),
				snmp.WithRetries(2),
			)
			if err != nil {
				b.Fatalf("native v3 dial: %v", err)
			}
			if _, err := sess.Get(ctx, []snmp.OID{scalar}); err != nil {
				b.Fatalf("native v3 Get: %v", err)
			}
			_ = sess.Close()
		}
	})

	b.Run("impl=gosnmp", func(b *testing.B) {
		host, port := gosnmpHostPort(b, c.agent)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			client := &g.GoSNMP{
				Target: host, Port: port,
				Version:       g.Version3,
				SecurityModel: g.UserSecurityModel,
				MsgFlags:      usm.gosnmpFlags(),
				SecurityParameters: &g.UsmSecurityParameters{
					UserName:                 usm.Username,
					AuthenticationProtocol:   usm.gosnmpAuth(),
					AuthenticationPassphrase: usm.AuthPassphrase,
					PrivacyProtocol:          usm.gosnmpPriv(),
					PrivacyPassphrase:        usm.PrivPassphrase,
				},
				Timeout: 3 * time.Second,
				Retries: 2,
			}
			if err := client.Connect(); err != nil {
				b.Fatalf("gosnmp v3 connect: %v", err)
			}
			if _, err := client.Get([]string{defaultScalarOID}); err != nil {
				b.Fatalf("gosnmp v3 Get: %v", err)
			}
			_ = client.Conn.Close()
		}
	})
}

// TestMacroDifferential doubles as the on-demand FlowSeer-vs-gosnmp
// differential that replaced the deleted t5 oracle: both walk the
// same root against the same live agent and must agree on the decoded
// OID sequence and the per-row SMI type.
//
// It deliberately does NOT assert per-row value equality: unlike the t5
// oracle (which diffed two backends against a static snmpsim replay),
// this tier runs two sequential walks against a LIVE agent, so any
// Counter/Gauge/TimeTicks column legitimately advances between the
// FlowSeer walk and the gosnmp walk. OID-sequence + type agreement is
// the robust differential here — it catches walk-shape and decode-type
// divergences without false-failing on monotonic counters.
func TestMacroDifferential(t *testing.T) {
	c := macroEnv(t)
	sess := dialNativeV2c(t, c)
	closeOnCleanup(t, sess)
	client := dialGosnmpV2c(t, c)
	closeOnCleanup(t, client.Conn)

	native := collectNativeRows(t, sess, mustRoot(t, c))
	gosnmp := collectGosnmpRows(t, client, c.walkRoot)

	if len(native) != len(gosnmp) {
		t.Fatalf("walk row-count divergence: flowseer=%d gosnmp=%d", len(native), len(gosnmp))
	}
	for i := range native {
		if native[i].oid != gosnmp[i].oid {
			t.Errorf("row %d OID divergence: flowseer=%s gosnmp=%s", i, native[i].oid, gosnmp[i].oid)
			continue
		}
		if native[i].class != gosnmp[i].class {
			t.Errorf("row %d (%s) type divergence: flowseer=%s gosnmp=%s",
				i, native[i].oid, native[i].class, gosnmp[i].class)
		}
	}
	t.Logf("differential OK: both walked %d rows under %s (OID + type)", len(native), c.walkRoot)
}

// diffRow is one decoded walk row reduced to the comparable fields the
// live differential checks: the dotted OID and a coarse SMI type class.
type diffRow struct {
	oid   string
	class string
}

func collectNativeRows(tb testing.TB, sess snmp.Session, root snmp.OID) []diffRow {
	tb.Helper()
	var rows []diffRow
	w := sess.BulkWalk(context.Background(), root)
	for oid, vb := range w.Iter() {
		rows = append(rows, diffRow{oid: oid.String(), class: nativeClass(vb)})
	}
	if err := w.Err(); err != nil {
		tb.Fatalf("native BulkWalk: %v", err)
	}
	return rows
}

func collectGosnmpRows(tb testing.TB, client *g.GoSNMP, root string) []diffRow {
	tb.Helper()
	res, err := client.BulkWalkAll(root)
	if err != nil {
		tb.Fatalf("gosnmp BulkWalkAll: %v", err)
	}
	rows := make([]diffRow, 0, len(res))
	for _, pdu := range res {
		rows = append(rows, diffRow{oid: strings.TrimPrefix(pdu.Name, "."), class: gosnmpClass(pdu.Type)})
	}
	return rows
}

func nativeClass(vb snmp.VarBind) string {
	switch vb.GetHeader().Kind {
	case snmp.KindInteger32:
		return "int"
	case snmp.KindCounter32:
		return "counter32"
	case snmp.KindGauge32:
		return "gauge32"
	case snmp.KindTimeTicks:
		return "timeticks"
	case snmp.KindCounter64:
		return "counter64"
	case snmp.KindUinteger32:
		return "uint32"
	case snmp.KindOctetString:
		return "octet"
	case snmp.KindObjectID:
		return "oid"
	case snmp.KindIPAddress:
		return "ipaddr"
	default:
		return vb.GetHeader().Kind.String()
	}
}

func gosnmpClass(t g.Asn1BER) string {
	switch t {
	case g.Integer:
		return "int"
	case g.Counter32:
		return "counter32"
	case g.Gauge32:
		return "gauge32"
	case g.TimeTicks:
		return "timeticks"
	case g.Counter64:
		return "counter64"
	case g.Uinteger32:
		return "uint32"
	case g.OctetString:
		return "octet"
	case g.ObjectIdentifier:
		return "oid"
	case g.IPAddress:
		return "ipaddr"
	default:
		return t.String()
	}
}

// TestMacroNetSnmp reports Net-SNMP wall-clock for the same workloads via
// hyperfine. It is a Test (not a Benchmark) because the work is a
// subprocess hyperfine times itself. Skips cleanly when the tools are
// absent.
func TestMacroNetSnmp(t *testing.T) {
	c := macroEnv(t)
	if !hasBin("hyperfine") {
		t.Skip("hyperfine not on PATH; skipping net-snmp arm")
	}
	if !hasBin("snmpbulkwalk") || !hasBin("snmpget") {
		t.Skip("net-snmp tools (snmpbulkwalk/snmpget) not on PATH")
	}

	walk, err := runHyperfine(t.Context(), snmpbulkwalkCmd(c.agent, c.community, c.walkRoot), 3, 20)
	if err != nil {
		t.Fatalf("hyperfine walk: %v", err)
	}
	t.Logf("net-snmp snmpbulkwalk %s: mean=%.1fms median=%.1fms p95=%.1fms min=%.1fms (n=%d)",
		c.walkRoot, walk.MeanMs, walk.MedianMs, walk.P95Ms, walk.MinMs, walk.Runs)

	get, err := runHyperfine(t.Context(), snmpgetCmd(c.agent, c.community, defaultScalarOID), 3, 20)
	if err != nil {
		t.Fatalf("hyperfine get: %v", err)
	}
	t.Logf("net-snmp snmpget %s: mean=%.1fms median=%.1fms p95=%.1fms min=%.1fms (n=%d) "+
		"(note: each run pays full process fork+exec — read as wall-clock-per-invocation, "+
		"not per-op latency)",
		defaultScalarOID, get.MeanMs, get.MedianMs, get.P95Ms, get.MinMs, get.Runs)
}

// benchUSM wraps snmp.USMConfig with the protocol names so the gosnmp
// client (which uses its own constant set) can be configured identically.
type benchUSM struct {
	snmp.USMConfig
	authProto string
	privProto string
}

func getenvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func v3USMFromEnv(tb testing.TB) (benchUSM, bool) {
	tb.Helper()
	user := os.Getenv("FLOWSEER_BENCH_V3_USER")
	if user == "" {
		return benchUSM{}, false
	}
	authProto := getenvOr("FLOWSEER_BENCH_V3_AUTHPROTO", "SHA")
	privProto := getenvOr("FLOWSEER_BENCH_V3_PRIVPROTO", "none")
	auth, ok := mapAuth(authProto)
	if !ok {
		tb.Fatalf("unknown FLOWSEER_BENCH_V3_AUTHPROTO %q (want SHA|SHA256|MD5)", authProto)
	}
	priv, ok := mapPriv(privProto)
	if !ok {
		tb.Fatalf("unknown FLOWSEER_BENCH_V3_PRIVPROTO %q (want AES|DES|none)", privProto)
	}
	return benchUSM{
		USMConfig: snmp.USMConfig{
			Username:       user,
			AuthProtocol:   auth,
			AuthPassphrase: os.Getenv("FLOWSEER_BENCH_V3_AUTHPASS"),
			PrivProtocol:   priv,
			PrivPassphrase: os.Getenv("FLOWSEER_BENCH_V3_PRIVPASS"),
		},
		authProto: authProto,
		privProto: privProto,
	}, true
}

func mapAuth(s string) (snmp.AuthProtocol, bool) {
	switch s {
	case "SHA":
		return snmp.AuthSHA, true
	case "SHA256":
		return snmp.AuthSHA256, true
	case "MD5":
		return snmp.AuthMD5, true
	default:
		return 0, false
	}
}

func mapPriv(s string) (snmp.PrivProtocol, bool) {
	switch s {
	case "AES":
		return snmp.PrivAES, true
	case "DES":
		return snmp.PrivDES, true
	case "none", "":
		return snmp.PrivProtocolNone, true
	default:
		return 0, false
	}
}

func (u benchUSM) gosnmpAuth() g.SnmpV3AuthProtocol {
	switch u.authProto {
	case "SHA256":
		return g.SHA256
	case "MD5":
		return g.MD5
	default:
		return g.SHA
	}
}

func (u benchUSM) gosnmpPriv() g.SnmpV3PrivProtocol {
	switch u.privProto {
	case "AES":
		return g.AES
	case "DES":
		return g.DES
	default:
		return g.NoPriv
	}
}

func (u benchUSM) gosnmpFlags() g.SnmpV3MsgFlags {
	switch {
	case u.privProto != "none" && u.privProto != "":
		return g.AuthPriv
	case u.authProto != "none" && u.authProto != "":
		return g.AuthNoPriv
	default:
		return g.NoAuthNoPriv
	}
}
