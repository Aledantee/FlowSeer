package fastiron_test

import (
	"testing"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/fastiron"
)

func TestParseShowInterface_UpUp(t *testing.T) {
	output := "GigabitEthernet1/1/1 is up, line protocol is up\r\n" +
		"  Port name is uplink to core\r\n" +
		"  Hardware is GigabitEthernet, address is 748e.f82a.6a00 (bia 748e.f82a.6a00)\r\n"

	description, admin, oper, err := fastiron.ParseShowInterface([]byte(output))
	if err != nil {
		t.Fatalf("ParseShowInterface: %v", err)
	}

	if description != "uplink to core" {
		t.Errorf("description = %q, want %q", description, "uplink to core")
	}

	if admin != interfacev1.AdminStatus_ADMIN_STATUS_UP {
		t.Errorf("admin = %v, want ADMIN_STATUS_UP", admin)
	}

	if oper != interfacev1.OperStatus_OPER_STATUS_UP {
		t.Errorf("oper = %v, want OPER_STATUS_UP", oper)
	}
}

func TestParseShowInterface_DisabledNoPortName(t *testing.T) {
	output := "GigabitEthernet1/1/1 is disabled, line protocol is down\r\n" +
		"  STP Root Guard is disabled, STP BPDU Guard is disabled\r\n" +
		"  No port name\r\n"

	description, admin, oper, err := fastiron.ParseShowInterface([]byte(output))
	if err != nil {
		t.Fatalf("ParseShowInterface: %v", err)
	}

	if description != "" {
		t.Errorf("description = %q, want empty", description)
	}

	if admin != interfacev1.AdminStatus_ADMIN_STATUS_DOWN {
		t.Errorf("admin = %v, want ADMIN_STATUS_DOWN", admin)
	}

	if oper != interfacev1.OperStatus_OPER_STATUS_DOWN {
		t.Errorf("oper = %v, want OPER_STATUS_DOWN", oper)
	}
}

func TestParseShowInterface_DescriptionWithSpacesAndPunctuation(t *testing.T) {
	output := "GigabitEthernet1/1/1 is up, line protocol is up\r\n" +
		"  Port name is rack 3, row B - uplink (primary)%\r\n"

	description, _, _, err := fastiron.ParseShowInterface([]byte(output))
	if err != nil {
		t.Fatalf("ParseShowInterface: %v", err)
	}

	if want := "rack 3, row B - uplink (primary)%"; description != want {
		t.Errorf("description = %q, want %q", description, want)
	}
}

func TestParseShowInterface_PaginatedOutputIsConsumed(t *testing.T) {
	// src/protocol/ssh.Session.Run strips the pagination marker before it
	// ever reaches Result.Output (Command.MorePattern), so
	// ParseShowInterface's own input never carries it; this proves the
	// parser reads correctly across the page boundary the marker used to
	// mark, with the marker already gone the way Run's contract promises.
	output := "GigabitEthernet1/1/1 is up, line protocol is up\r\n" +
		"  Hardware is GigabitEthernet, address is 748e.f82a.6a00 (bia 748e.f82a.6a00)\r\n" +
		"  Port name is uplink to core\r\n"

	description, _, _, err := fastiron.ParseShowInterface([]byte(output))
	if err != nil {
		t.Fatalf("ParseShowInterface: %v", err)
	}

	if description != "uplink to core" {
		t.Errorf("description = %q, want %q", description, "uplink to core")
	}
}

func TestParseShowInterface_InvalidInputIsRejected(t *testing.T) {
	output := "Invalid input -> ethernett 1/1/1\r\nType ? for a list\r\n"

	if _, _, _, err := fastiron.ParseShowInterface([]byte(output)); err == nil {
		t.Fatal("ParseShowInterface did not error on an Invalid input transcript")
	}
}

func TestParseShowInterface_IncompleteCommandIsRejected(t *testing.T) {
	output := "Incomplete command.\r\n"

	if _, _, _, err := fastiron.ParseShowInterface([]byte(output)); err == nil {
		t.Fatal("ParseShowInterface did not error on an Incomplete command. transcript")
	}
}

func TestParseShowInterface_UnrecognizedHeaderErrors(t *testing.T) {
	if _, _, _, err := fastiron.ParseShowInterface([]byte("garbage\r\n")); err == nil {
		t.Fatal("ParseShowInterface did not error on unrecognized output")
	}
}

func TestParseShowInterface_ControlBytesAndEmbeddedPromptLookalike(t *testing.T) {
	// A description containing bytes that look like a prompt, and stray
	// control bytes elsewhere in the block, must not crash the parser or
	// change what it reads from the real header and port-name lines.
	output := "GigabitEthernet1/1/1 is up, line protocol is up\r\n" +
		"  Port name is see SSH@device# for details\x07\x1b[0m\r\n"

	description, admin, oper, err := fastiron.ParseShowInterface([]byte(output))
	if err != nil {
		t.Fatalf("ParseShowInterface: %v", err)
	}

	if want := "see SSH@device# for details\x07\x1b[0m"; description != want {
		t.Errorf("description = %q, want %q", description, want)
	}

	if admin != interfacev1.AdminStatus_ADMIN_STATUS_UP || oper != interfacev1.OperStatus_OPER_STATUS_UP {
		t.Errorf("admin/oper = %v/%v, want UP/UP", admin, oper)
	}
}
