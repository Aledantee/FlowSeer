package inventory

import (
	"bufio"
	"os"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func parseGoSum(path, manifest, rootModule string, direct map[string]bool) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errs.Wrap(err, "open Go checksum file")
	}
	defer func() { _ = file.Close() }()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		if ownedModule(fields[0], rootModule) {
			continue
		}
		entries = append(entries, Entry{
			Ecosystem: "go",
			Name:      fields[0],
			Version:   fields[1],
			Hash:      fields[2],
			Manifests: []string{manifest},
			Direct:    direct[fields[0]],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, errs.Wrap(err, "read Go checksum file")
	}
	return entries, nil
}
