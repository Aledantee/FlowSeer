package snmp

import "testing"

func TestOIDSet_Longest(t *testing.T) {
	enterprises := MustOID(1, 3, 6, 1, 4, 1)
	cisco := enterprises.Append(9)
	ciscoProducts := cisco.Append(1)
	catalyst := ciscoProducts.Append(516)
	set := NewOIDSet([]OID{catalyst, cisco, enterprises.Append(2011), ciscoProducts, cisco})

	if got := set.Len(); got != 4 {
		t.Fatalf("Len = %d, want duplicates collapsed to 4", got)
	}

	tests := []struct {
		name  string
		probe OID
		want  OID
		found bool
	}{
		{"deepest entry", catalyst.Append(3), catalyst, true},
		{"exact entry", ciscoProducts, ciscoProducts, true},
		{"skips deeper siblings", ciscoProducts.Append(9999), ciscoProducts, true},
		{"falls back to shallower entry", cisco.Append(5, 1), cisco, true},
		{"unknown root", MustOID(1, 3, 6, 1, 4, 1, 4242, 1), OID{}, false},
		{"above every entry", MustOID(1, 3, 6, 1, 4), OID{}, false},
		{"other vendor", MustOID(1, 3, 6, 1, 4, 1, 2011, 2, 23), enterprises.Append(2011), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := set.Longest(tc.probe)
			if found != tc.found || !got.Equal(tc.want) {
				t.Fatalf("Longest(%s) = %s, %v; want %s, %v", tc.probe, got, found, tc.want, tc.found)
			}
		})
	}
}

func TestOIDSet_Empty(t *testing.T) {
	var set OIDSet
	if got, found := set.Longest(MustOID(1, 3, 6)); found || got.Len() != 0 {
		t.Fatalf("Longest on empty set = %s, %v", got, found)
	}
	if NewOIDSet(nil).Len() != 0 {
		t.Fatal("NewOIDSet(nil) is not empty")
	}
	if NewOIDSet([]OID{{}}).Len() != 0 {
		t.Fatal("the empty OID is never a member")
	}
}

func TestOIDSet_ConstructionDoesNotAliasInput(t *testing.T) {
	in := []OID{MustOID(1, 3, 6, 1, 4, 1, 9), MustOID(1, 3, 6, 1, 4, 1, 2)}
	set := NewOIDSet(in)
	in[0], in[1] = in[1], in[0]
	got, found := set.Longest(MustOID(1, 3, 6, 1, 4, 1, 9, 1))
	if !found || got.String() != "1.3.6.1.4.1.9" {
		t.Fatalf("Longest = %s, %v", got, found)
	}
}
