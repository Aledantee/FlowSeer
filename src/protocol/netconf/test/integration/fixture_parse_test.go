package integration

import "testing"

func TestNativeUserExists(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want bool
		err  bool
	}{
		{name: "empty"},
		{name: "existing", xml: `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><username><name>flowseer-t4</name></username></native>`, want: true},
		{name: "similar_name", xml: `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><username><name>flowseer-t4-backup</name></username></native>`},
		{name: "description", xml: `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><description>flowseer-t4</description></native>`},
		{name: "malformed", xml: `<native`, err: true},
		{name: "wrong_root", xml: `<native xmlns="another-module"/>`, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nativeUserExists([]byte(tc.xml), "flowseer-t4")
			if (err != nil) != tc.err {
				t.Fatalf("parse error = %v, want error %t", err, tc.err)
			}
			if got != tc.want {
				t.Errorf("got user exists %t, want %t", got, tc.want)
			}
		})
	}
}
