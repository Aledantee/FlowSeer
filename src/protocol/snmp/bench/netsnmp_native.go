//go:build snmp_bench_netsnmp

package bench

// netsnmp_native.go binds the Net-SNMP C library (libnetsnmp) in-process via
// cgo, so the third comparand is the real Net-SNMP SNMP engine — NOT the CLI
// tools the macro tier shells out to. The CLI pays a full process
// fork+exec+init per invocation (~20 ms on this host); these bindings call
// snmp_sess_synch_response directly, the same library path a long-lived C
// poller would use, making the per-operation comparison against the FlowSeer
// and gosnmp Go libraries apples-to-apples.
//
// Build-tagged (snmp_bench_netsnmp) and cgo: it only compiles when explicitly
// requested and when libnetsnmp dev headers are present. Install on macOS with
// `brew install net-snmp`; the cgo paths below point at the Homebrew opt
// prefix. On other layouts, override with CGO_CFLAGS / CGO_LDFLAGS.
//
// cgo cannot live in _test.go files, so the binding is here; the smoke test
// and the sweep harness that drive it are in netsnmp_native_test.go /
// sweep_test.go under the same build tag.
//
// Caveat for -benchmem: Go's allocator only counts Go-heap allocations, so the
// reported allocs/op and B/op for the Net-SNMP arm reflect only the thin cgo
// marshaling, not Net-SNMP's (substantial) C-side malloc traffic. Compare the
// Net-SNMP arm on throughput (ops/s, ns/op) only; the allocation columns are
// meaningful for the two Go clients.

/*
#cgo CFLAGS: -I/opt/homebrew/opt/net-snmp/include -I/opt/homebrew/opt/openssl@3/include
#cgo LDFLAGS: -L/opt/homebrew/opt/net-snmp/lib -lnetsnmp
#include <net-snmp/net-snmp-config.h>
#include <net-snmp/net-snmp-includes.h>
#include <stdlib.h>
#include <string.h>

// ns_init initializes the library once. MIB file parsing is disabled (we use
// numeric OIDs only) to keep startup fast and deterministic.
static void ns_init(void) {
    setenv("MIBS", "", 1);
    init_snmp("flowseer-bench-netsnmp");
}

// ns_open opens a v2c single-session to peer ("host:port") with community,
// matching the FlowSeer/gosnmp dialers' 2s timeout and 3 retries. Returns an
// opaque session handle, or NULL on failure.
static void *ns_open(const char *peer, const char *community, long timeout_us, int retries) {
    netsnmp_session sess;
    snmp_sess_init(&sess);
    sess.version = SNMP_VERSION_2c;
    sess.peername = (char *)peer;
    sess.community = (u_char *)community;
    sess.community_len = strlen(community);
    sess.timeout = timeout_us;
    sess.retries = retries;
    return snmp_sess_open(&sess);
}

static void ns_close(void *sessp) {
    if (sessp != NULL) {
        snmp_sess_close(sessp);
    }
}

// ns_bulkwalk walks the subtree rooted at root[0..rootLen) with repeated
// GETBULK (non-repeaters=0, max-repetitions=maxrep), following the response
// cursor until the walk leaves the subtree or hits an end-of-MIB / exception
// marker — the same traversal snmpbulkwalk performs. Returns the number of
// in-subtree varbinds collected, or -1 on a transport/protocol error.
static long ns_bulkwalk(void *sessp, oid *root, size_t rootLen, int maxrep) {
    oid cur[MAX_OID_LEN];
    size_t curLen = rootLen;
    if (rootLen == 0 || rootLen > MAX_OID_LEN || maxrep <= 0) {
        return -1;
    }
    memcpy(cur, root, rootLen * sizeof(oid));

    long count = 0;
    int running = 1;
    while (running) {
        netsnmp_pdu *pdu = snmp_pdu_create(SNMP_MSG_GETBULK);
        if (pdu == NULL) {
            return -1;
        }
        pdu->non_repeaters = 0;
        pdu->max_repetitions = maxrep;
        if (snmp_add_null_var(pdu, cur, curLen) == NULL) {
            snmp_free_pdu(pdu);
            return -1;
        }

        netsnmp_pdu *resp = NULL;
        int status = snmp_sess_synch_response(sessp, pdu, &resp);
        if (status != STAT_SUCCESS || resp == NULL) {
            if (resp != NULL) {
                snmp_free_pdu(resp);
            }
            return -1;
        }
        if (resp->errstat != SNMP_ERR_NOERROR || resp->variables == NULL) {
            snmp_free_pdu(resp);
            return -1;
        }

        netsnmp_variable_list *vb;
        for (vb = resp->variables; vb != NULL; vb = vb->next_variable) {
            if (vb->type == SNMP_ENDOFMIBVIEW ||
                vb->type == SNMP_NOSUCHOBJECT ||
                vb->type == SNMP_NOSUCHINSTANCE) {
                running = 0;
                break;
            }
            // Left the requested subtree?
            if (vb->name_length < rootLen ||
                memcmp(vb->name, root, rootLen * sizeof(oid)) != 0) {
                running = 0;
                break;
            }
            // A non-advancing cursor would otherwise repeat requests forever.
            if (vb->name_length > MAX_OID_LEN ||
                snmp_oid_compare(vb->name, vb->name_length, cur, curLen) <= 0) {
                snmp_free_pdu(resp);
                return -1;
            }
            count++;
            memcpy(cur, vb->name, vb->name_length * sizeof(oid));
            curLen = vb->name_length;
        }
        snmp_free_pdu(resp);
    }
    return count;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// nsInitOnce runs the one-time library init. snmp_sess_open touches global
// state during setup, so opens are also serialized (see nsOpen).
var (
	nsInitOnce sync.Once
	nsOpenMu   sync.Mutex
)

// walkBulkMaxRep is the GETBULK max-repetitions the Net-SNMP arm uses, kept
// equal to FlowSeer's walkBulkMaxRepetitions and gosnmp's defaultMaxRepetitions
// (both 50) so every client issues the same number of round-trips per walk.
const walkBulkMaxRep = 50

// nsSession is a live Net-SNMP single-session handle.
type nsSession struct{ p unsafe.Pointer }

// nsOpen dials a v2c Net-SNMP session to addr ("host:port") with community,
// using the same 2s timeout / 3 retries as the Go dialers.
func nsOpen(addr, community string) (nsSession, error) {
	nsInitOnce.Do(func() { C.ns_init() })

	cPeer := C.CString(addr)
	defer C.free(unsafe.Pointer(cPeer))
	cComm := C.CString(community)
	defer C.free(unsafe.Pointer(cComm))

	nsOpenMu.Lock()
	p := C.ns_open(cPeer, cComm, C.long(2_000_000), C.int(3))
	nsOpenMu.Unlock()
	if p == nil {
		return nsSession{}, fmt.Errorf("net-snmp: snmp_sess_open(%s) failed", addr)
	}
	return nsSession{p: p}, nil
}

func (s nsSession) close() { C.ns_close(s.p) }

// bulkWalk walks root via Net-SNMP GETBULK and returns the in-subtree varbind
// count. The traversal runs entirely in C (one blocking cgo call per round).
func (s nsSession) bulkWalk(root []uint32, maxRep int) (int, error) {
	coid := make([]C.oid, len(root))
	for i, v := range root {
		coid[i] = C.oid(v)
	}
	var first *C.oid
	if len(coid) > 0 {
		first = &coid[0]
	}
	n := C.ns_bulkwalk(s.p, first, C.size_t(len(coid)), C.int(maxRep))
	if n < 0 {
		return 0, fmt.Errorf("net-snmp bulkwalk failed")
	}
	return int(n), nil
}

// oidSubs extracts the sub-identifiers of a snmp.OID for the C binding.
func oidSubs(o snmp.OID) []uint32 {
	subs := make([]uint32, o.Len())
	for i := range subs {
		subs[i] = o.At(i)
	}
	return subs
}
