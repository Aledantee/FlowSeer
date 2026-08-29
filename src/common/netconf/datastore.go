package netconf

import "strings"

// Datastore names an RFC 6241 configuration datastore.
type Datastore string

// The datastores the library addresses. :startup is out of scope for
// v1.
const (
	Running   Datastore = "running"
	Candidate Datastore = "candidate"
)

// Capability URN prefixes (RFC 6241 §8; URNs may carry query
// parameters, so matching is prefix-based).
const (
	capCandidate       = "urn:ietf:params:netconf:capability:candidate:1.0"
	capWritableRunning = "urn:ietf:params:netconf:capability:writable-running:1.0"
	capValidate        = "urn:ietf:params:netconf:capability:validate:1."
	capRollbackOnError = "urn:ietf:params:netconf:capability:rollback-on-error:1.0"
)

// capabilities is the parsed hello: the raw set plus the derived
// datastore facts the edit flow keys on.
type capabilities struct {
	all             []string
	candidate       bool
	writableRunning bool
	validate        bool
	rollbackOnError bool
}

// parseCapabilities derives the datastore facts from the server's
// advertised capability URIs.
func parseCapabilities(caps []string) capabilities {
	out := capabilities{all: caps}
	for _, c := range caps {
		switch {
		case strings.HasPrefix(c, capCandidate):
			out.candidate = true
		case strings.HasPrefix(c, capWritableRunning):
			out.writableRunning = true
		case strings.HasPrefix(c, capValidate):
			out.validate = true
		case strings.HasPrefix(c, capRollbackOnError):
			out.rollbackOnError = true
		}
	}
	return out
}

// editTarget selects the datastore edits address: candidate wins
// (IOS-XE candidate mode makes running non-writable), then writable
// running; neither means the peer cannot be edited over NETCONF.
func (c capabilities) editTarget() (Datastore, bool) {
	switch {
	case c.candidate:
		return Candidate, true
	case c.writableRunning:
		return Running, true
	default:
		return "", false
	}
}
