package trace

import (
	"fmt"
	"testing"
)

type testStatusFact struct {
	status string
}

func (f testStatusFact) TypeID() string {
	return "admin_status"
}

func (f testStatusFact) Canonical() string {
	return f.status
}

func TestSortChanges(t *testing.T) {
	changes := []Change{
		{
			Layer:   "vlan",
			Subject: Subject{Kind: "vlan", Key: "20"},
			Field:   "name",
			From:    nil,
			To:      testStatusFact{status: "engineering"},
		},
		{
			Layer:   "port",
			Subject: Subject{Kind: "port", Key: "1/1/2"},
			Field:   "admin_status",
			From:    testStatusFact{status: "Down"},
			To:      testStatusFact{status: "Up"},
		},
		{
			Layer:   "port",
			Subject: Subject{Kind: "port", Key: "1/1/1"},
			Field:   "admin_status",
			From:    testStatusFact{status: "Down"},
			To:      testStatusFact{status: "Up"},
		},
		{
			Layer:   "bridge",
			Subject: Subject{Kind: "bridge", Key: "br0"},
			Field:   "aging_time",
			From:    nil,
			To:      testStatusFact{status: "300"},
		},
	}

	sortChanges(changes)

	expectedOrder := []string{
		"bridge:bridge:br0:aging_time",
		"port:port:1/1/1:admin_status",
		"port:port:1/1/2:admin_status",
		"vlan:vlan:20:name",
	}

	for i, c := range changes {
		key := fmt.Sprintf("%s:%s:%s", c.Layer, c.Subject.String(), c.Field)
		if key != expectedOrder[i] {
			t.Errorf("changes[%d] = %q, want %q", i, key, expectedOrder[i])
		}
	}
}
