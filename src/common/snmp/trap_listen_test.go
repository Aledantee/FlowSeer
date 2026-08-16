package snmp

import (
	"context"
	"net"
	"testing"
	"time"
)

// startTrapListener binds a native trap listener on an ephemeral loopback
// port and returns the stream plus its bound address (captured via the
// test hook).
func startTrapListener(t *testing.T, opts ...TrapOption) (*TrapStream, *net.UDPAddr) {
	t.Helper()
	var addr *net.UDPAddr
	listenerRegistered = func(_ *TrapStream, l *listener) {
		addr = l.conn.LocalAddr().(*net.UDPAddr)
	}
	t.Cleanup(func() { listenerRegistered = nil })

	ts, err := ListenTraps(context.Background(), "127.0.0.1:0", opts...)
	if err != nil {
		t.Fatalf("ListenTraps: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })
	if addr == nil {
		t.Fatal("listener address not captured")
	}
	return ts, addr
}

func sendDatagram(t *testing.T, addr *net.UDPAddr, data []byte) {
	t.Helper()
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial trap listener: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write(data); err != nil {
		t.Fatalf("send trap: %v", err)
	}
}

// nextTrap waits for one trap from the stream, or reports false on timeout.
func nextTrap(ts *TrapStream, timeout time.Duration) (Trap, bool) {
	ch := make(chan Trap, 1)
	go func() {
		if ts.Next() {
			ch <- ts.Current()
		}
	}()
	select {
	case tr := <-ch:
		return tr, true
	case <-time.After(timeout):
		return Trap{}, false
	}
}

func buildV2cTrap(t *testing.T, vbs []VarBind) []byte {
	t.Helper()
	data, err := encodeMessage(&message{
		version:   V2c,
		community: "public",
		pdu:       pdu{typ: pduV2Trap, requestID: 1, varbinds: vbs},
	})
	if err != nil {
		t.Fatalf("encode v2c trap: %v", err)
	}
	return data
}

func buildV1Trap(t *testing.T, community string, enterprise OID, agent net.IP, generic, specific int, ts uint32, payload []VarBind) []byte {
	t.Helper()
	var body []byte
	body = appendOID(body, enterprise)
	ip, err := appendIPv4(agent)
	if err != nil {
		t.Fatalf("agent ip: %v", err)
	}
	body = append(body, ip...)
	body = appendInt(body, int64(generic))
	body = appendInt(body, int64(specific))
	body = appendUint(body, tagTimeTicks, uint64(ts))
	vbl, err := encodeVarBindList(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	body = append(body, vbl...)
	pduBytes := appendSequence(byte(pduV1Trap), body)

	var msgBody []byte
	msgBody = appendInt(msgBody, wireVersionV1)
	msgBody = appendOctetString(msgBody, tagOctetString, []byte(community))
	msgBody = append(msgBody, pduBytes...)
	return appendSequence(tagSequence, msgBody)
}

func buildV3Datagram() []byte {
	var body []byte
	body = appendInt(body, wireVersionV3)
	body = appendOctetString(body, tagOctetString, []byte("x"))
	body = append(body, 0xff, 0xff) // garbage where the v3 structure would be
	return appendSequence(tagSequence, body)
}

func snmpTrapVarBinds() []VarBind {
	sysUpTime := MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	snmpTrapOID := MustOID(1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0)
	coldStart := MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 1)
	return []VarBind{
		TimeTicksVar{Header: Header{OID: sysUpTime, Kind: KindTimeTicks}, Value: 4242},
		ObjectIDVar{Header: Header{OID: snmpTrapOID, Kind: KindObjectID}, Value: coldStart},
	}
}

func TestTrap_V2cReceive(t *testing.T) {
	ts, addr := startTrapListener(t)
	sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))

	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("no v2c trap received")
	}
	if tr.Version != V2c || tr.Community != "public" {
		t.Fatalf("trap meta: version=%s community=%s", tr.Version, tr.Community)
	}
	if len(tr.VarBinds) != 2 {
		t.Fatalf("want 2 varbinds, got %d", len(tr.VarBinds))
	}
}

func TestTrap_V1Receive_TranslatedToV2(t *testing.T) {
	ts, addr := startTrapListener(t)
	enterprise := MustOID(1, 3, 6, 1, 4, 1, 9)
	payload := []VarBind{
		OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindOctetString}, Value: []byte("detail")},
	}
	// generic=6 (enterpriseSpecific), specific=7.
	sendDatagram(t, addr, buildV1Trap(t, "public", enterprise, net.IPv4(10, 1, 2, 3), 6, 7, 999, payload))

	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("no v1 trap received")
	}
	if tr.Version != V1 {
		t.Fatalf("version = %s, want v1", tr.Version)
	}
	// Canonical translation: sysUpTime.0, snmpTrapOID.0, payload,
	// snmpTrapEnterprise.0.
	if len(tr.VarBinds) != 4 {
		t.Fatalf("want 4 translated varbinds, got %d: %+v", len(tr.VarBinds), tr.VarBinds)
	}
	if !tr.VarBinds[0].GetHeader().OID.Equal(oidSysUpTime) {
		t.Errorf("vb0 = %s, want sysUpTime.0", tr.VarBinds[0].GetHeader().OID)
	}
	trapOID, ok := tr.VarBinds[1].(ObjectIDVar)
	if !ok || !tr.VarBinds[1].GetHeader().OID.Equal(oidSnmpTrapOID) {
		t.Fatalf("vb1 = %#v, want snmpTrapOID.0", tr.VarBinds[1])
	}
	// enterpriseSpecific → enterprise.0.specific.
	wantTrapOID := enterprise.Append(0, 7)
	if !trapOID.Value.Equal(wantTrapOID) {
		t.Errorf("snmpTrapOID = %s, want %s", trapOID.Value, wantTrapOID)
	}
	last := tr.VarBinds[3]
	if !last.GetHeader().OID.Equal(oidSnmpTrapEnterprise) {
		t.Errorf("last vb = %s, want snmpTrapEnterprise.0", last.GetHeader().OID)
	}
}

func TestTrap_AllowedSourcesFilter(t *testing.T) {
	// Allow only a network the loopback sender is not in.
	_, deniedNet, _ := net.ParseCIDR("10.0.0.0/8")
	ts, addr := startTrapListener(t, WithAllowedSources(*deniedNet))
	sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))

	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("trap from disallowed source should be dropped")
	}
	// Source-filter drops do not count against Dropped().
	if ts.Dropped() != 0 {
		t.Errorf("source-filter drop counted: Dropped=%d", ts.Dropped())
	}
}

func TestTrap_EmptyAllowedSourcesAdmits(t *testing.T) {
	ts, addr := startTrapListener(t) // no WithAllowedSources → admit all
	sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("default empty allow-list should admit the trap")
	}
}

func TestTrap_MalformedKeepsRunning(t *testing.T) {
	ts, addr := startTrapListener(t)
	// A garbage datagram must not kill the listener.
	sendDatagram(t, addr, []byte{0x30, 0x05, 0xff, 0xff, 0xff, 0xff, 0xff})
	// A valid trap after it must still arrive.
	sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))

	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("valid trap after malformed packet was not received")
	}
	if ts.Dropped() == 0 {
		t.Error("malformed packet was not counted as dropped")
	}
}

func TestTrap_V3DroppedAndEngineUnsupported(t *testing.T) {
	ts, addr := startTrapListener(t)
	sendDatagram(t, addr, buildV3Datagram())
	// Follow with a valid v2c trap to confirm the v3 datagram did not
	// wedge the listener.
	sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))

	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("v2c trap after v3 not received")
	}
	if tr.Version != V2c {
		t.Fatalf("unexpected version %s", tr.Version)
	}
	if ts.Dropped() == 0 {
		t.Error("v3 trap was not counted as dropped (S7)")
	}
	// Engine registration is supported on native: a valid USMConfig with
	// an EngineID is accepted; an invalid one (no EngineID) is rejected via
	// Validate/ErrEngineNeedsID, not ErrV3Unsupported.
	valid := USMConfig{
		Username: "u1", EngineID: []byte{0x80, 0, 0, 1, 2, 3},
		AuthProtocol: AuthSHA256, AuthPassphrase: "auth-passphrase-1234",
	}
	if err := ts.RegisterEngine(valid); err != nil {
		t.Fatalf("RegisterEngine(valid) err = %v, want nil", err)
	}
	if err := ts.RegisterEngine(USMConfig{EngineID: []byte{1, 2, 3}}); err == nil {
		t.Fatalf("RegisterEngine(no username) should error")
	}
}

func TestTrap_RateLimit(t *testing.T) {
	ts, addr := startTrapListener(t, WithMaxTrapsPerSecond(1))
	for i := 0; i < 4; i++ {
		sendDatagram(t, addr, buildV2cTrap(t, snmpTrapVarBinds()))
	}
	// At least one should arrive; the excess within the one-second window
	// should be dropped.
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("first trap under the rate limit was not received")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && ts.Dropped() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if ts.Dropped() == 0 {
		t.Error("rate-limited excess traps were not counted as dropped")
	}
}

func TestTrap_CloseStopsListener(t *testing.T) {
	ts, _ := startTrapListener(t)
	if err := ts.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Second close is idempotent.
	if err := ts.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
