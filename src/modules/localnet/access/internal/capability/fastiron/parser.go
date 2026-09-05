package fastiron

import (
	"bytes"
	"regexp"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeUnparseable identifies "show interfaces" output whose header or
// description line this parser does not recognize.
var ErrCodeUnparseable = errs.NewCode("fastiron/unparseable")

// ErrCodeCommandRejected identifies output that is an "Invalid input" or
// "Incomplete command." transcript instead of the block ParseShowInterface
// expects: the device rejected the command, so this is never mistaken for
// a successful read.
var ErrCodeCommandRejected = errs.NewCode("fastiron/command-rejected")

// headerPattern matches "show interfaces"' first line: "<IfName> is
// {up|down|disabled}, line protocol is {up|down}", the wording FastIron
// documents
// (https://docs.ruckuswireless.com/fastiron/08.0.60/fastiron-08060-commandref/GUID-54E45EEA-5E28-49B9-B4C7-DCA7811947F6.html):
// "GigabitEthernet1/1/1 is up, line protocol is up" for an enabled,
// linked-up port and "GigabitEthernet1/1/1 is disabled, line protocol is
// down" for an administratively disabled one.
var headerPattern = regexp.MustCompile(`(?m)^\S+ is (up|down|disabled), line protocol is (up|down)\s*$`)

// noPortNamePattern and portNamePattern match the configured description
// line: "No port name" when none is set, or "Port name is <text>" with the
// rest of the line verbatim — both wordings the same reference page's
// examples show.
var (
	noPortNamePattern = regexp.MustCompile(`(?m)^\s*No port name\s*$`)
	portNamePattern   = regexp.MustCompile(`(?m)^\s*Port name is (.*)$`)
)

// ParseShowInterface parses a ShowInterfaceCommand's output into an
// interface's description, admin status, and oper status. It never
// guesses: an Invalid input/Incomplete command. transcript returns
// ErrCodeCommandRejected before any other pattern is tried, and an
// unrecognized header or a header with no description line at all (every
// real "show interfaces" block prints one) returns ErrCodeUnparseable.
func ParseShowInterface(output []byte) (description string, admin interfacev1.AdminStatus, oper interfacev1.OperStatus, err error) {
	if invalidInputPattern.Match(output) {
		return "", interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED, interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED,
			errs.New().Code(ErrCodeCommandRejected).Msg("device rejected the command")
	}

	header := headerPattern.FindSubmatch(output)
	if header == nil {
		return "", interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED, interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED,
			errs.New().Code(ErrCodeUnparseable).Msg("output does not match a show interfaces header")
	}

	if string(header[1]) == "disabled" {
		admin = interfacev1.AdminStatus_ADMIN_STATUS_DOWN
	} else {
		admin = interfacev1.AdminStatus_ADMIN_STATUS_UP
	}

	if string(header[2]) == "up" {
		oper = interfacev1.OperStatus_OPER_STATUS_UP
	} else {
		oper = interfacev1.OperStatus_OPER_STATUS_DOWN
	}

	switch {
	case noPortNamePattern.Match(output):
		description = ""
	default:
		m := portNamePattern.FindSubmatch(output)
		if m == nil {
			return "", interfacev1.AdminStatus_ADMIN_STATUS_UNSPECIFIED, interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED,
				errs.New().Code(ErrCodeUnparseable).Msg("output has no port-name line")
		}

		description = string(bytes.TrimRight(m[1], "\r"))
	}

	return description, admin, oper, nil
}
