package findings_test

import (
	"encoding/json"
	"fmt"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

func ExampleNewSecret() {
	detail, err := json.Marshal(struct {
		Secrets []findings.Secret `json:"secrets"`
	}{
		Secrets: []findings.Secret{findings.NewSecret("snmp", []byte("fixture"))},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	record := findings.NewRecord(findings.KindFinding)
	record.Finding = &findings.Finding{Module: "snmp", Detail: detail}
	fmt.Println(string(record.Finding.Detail))
	// Output: {"secrets":[{"protocol":"snmp","length":7}]}
}
