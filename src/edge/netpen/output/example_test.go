package output_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/output"
)

func ExampleJSONWriter() {
	var stdout bytes.Buffer
	w := output.NewJSONWriter(&stdout, io.Discard, findings.Meta{Tool: "netpen"})
	summary := findings.NewRecord(findings.KindSummary)
	summary.Summary = &findings.Summary{}
	if err := w.Run([]findings.Record{summary}); err != nil {
		fmt.Println(err)
		return
	}

	decoder := json.NewDecoder(&stdout)
	for range 2 {
		var record findings.Record
		if err := decoder.Decode(&record); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(record.Kind)
	}
	// Output:
	// meta
	// summary
}
