package netconf

import "encoding/xml"

// rpc.go defines the RFC 6241 operation payloads the session sends
// through the transport seam. All are unexported: callers speak
// [Session] methods, never raw operations.

// datastoreElem renders <running/> / <candidate/> inside source and
// target wrappers.
type datastoreElem struct {
	Running   *struct{} `xml:"running,omitempty"`
	Candidate *struct{} `xml:"candidate,omitempty"`
}

// dsElem builds the element for ds.
func dsElem(ds Datastore) datastoreElem {
	switch ds {
	case Candidate:
		return datastoreElem{Candidate: &struct{}{}}
	default:
		return datastoreElem{Running: &struct{}{}}
	}
}

// subtreeFilter is the RFC 6241 §6 filter element with inner subtree
// XML.
type subtreeFilter struct {
	XMLName xml.Name `xml:"filter"`
	Type    string   `xml:"type,attr"`
	Inner   []byte   `xml:",innerxml"`
}

// newSubtreeFilter wraps rendered filter XML; nil inner means no
// filter element at all.
func newSubtreeFilter(inner []byte) *subtreeFilter {
	if len(inner) == 0 {
		return nil
	}
	return &subtreeFilter{Type: "subtree", Inner: inner}
}

type getOp struct {
	XMLName xml.Name `xml:"get"`
	Filter  *subtreeFilter
}

type getConfigOp struct {
	XMLName xml.Name      `xml:"get-config"`
	Source  datastoreElem `xml:"source"`
	Filter  *subtreeFilter
}

type editConfigOp struct {
	XMLName          xml.Name      `xml:"edit-config"`
	Target           datastoreElem `xml:"target"`
	DefaultOperation string        `xml:"default-operation,omitempty"`
	ErrorOption      string        `xml:"error-option,omitempty"`
	Config           editConfig    `xml:"config"`
}

type editConfig struct {
	Inner []byte `xml:",innerxml"`
}

type lockOp struct {
	XMLName xml.Name      `xml:"lock"`
	Target  datastoreElem `xml:"target"`
}

type unlockOp struct {
	XMLName xml.Name      `xml:"unlock"`
	Target  datastoreElem `xml:"target"`
}

type validateOp struct {
	XMLName xml.Name      `xml:"validate"`
	Source  datastoreElem `xml:"source"`
}

type commitOp struct {
	XMLName xml.Name `xml:"commit"`
}

type discardChangesOp struct {
	XMLName xml.Name `xml:"discard-changes"`
}

// dataReply captures a <data> payload from get / get-config replies;
// the inner XML feeds the generated codecs.
type dataReply struct {
	Data struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"data"`
}
