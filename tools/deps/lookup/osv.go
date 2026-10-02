// Package lookup enriches dependency inventory entries with advisory and
// publication data from the upstream services named by the command.
//
// A live OSV query on 2026-10-01 matched the real pseudo-version
// v0.0.0-20210220033148-5ea612d1eb83 of golang.org/x/crypto to GO-2026-5932.
// The query used the version without a leading v, as OSV's Go query accepts.
package lookup

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

const batchSize = 200

// Advisory is one OSV advisory matched to an inventory entry.
type Advisory struct {
	Ecosystem string   `json:"ecosystem"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	Imports   []string `json:"imports"`
}

type osvQuery struct {
	Package   osvPackage `json:"package"`
	Version   string     `json:"version"`
	PageToken string     `json:"page_token,omitempty"`
}

type osvPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type osvBatchResponse struct {
	Results []osvResult `json:"results"`
}

type osvResult struct {
	Vulnerabilities []osvReference `json:"vulns"`
	NextPageToken   string         `json:"next_page_token"`
}

type osvReference struct {
	ID string `json:"id"`
}

type osvDetail struct {
	ID       string        `json:"id"`
	Summary  string        `json:"summary"`
	Affected []osvAffected `json:"affected"`
}

type osvAffected struct {
	EcosystemSpecific osvEcosystemSpecific `json:"ecosystem_specific"`
}

type osvEcosystemSpecific struct {
	Imports []osvImport `json:"imports"`
}

type osvImport struct {
	Path string `json:"path"`
}

type queryWork struct {
	Index int
	Query osvQuery
}

// Advisories queries OSV in batches of 200 and follows each result's page
// token. The detail endpoint is fetched once for every distinct advisory ID.
func Advisories(ctx context.Context, entries []inventory.Entry, batchURL, detailBaseURL string) ([]Advisory, error) {
	ids, err := queryAdvisoryIDs(ctx, entries, batchURL)
	if err != nil {
		return nil, err
	}

	details := make(map[string]osvDetail)
	for _, entryIDs := range ids {
		for _, id := range entryIDs {
			if _, ok := details[id]; ok {
				continue
			}
			var detail osvDetail
			endpoint := strings.TrimRight(detailBaseURL, "/") + "/" + url.PathEscape(id)
			if err := getJSON(ctx, http.DefaultClient, endpoint, &detail); err != nil {
				return nil, errs.Wrapf(err, "fetch OSV advisory %s", id)
			}
			details[id] = detail
		}
	}

	var advisories []Advisory
	for index, entryIDs := range ids {
		for _, id := range entryIDs {
			detail := details[id]
			imports := make([]string, 0)
			for _, affected := range detail.Affected {
				for _, imported := range affected.EcosystemSpecific.Imports {
					if imported.Path != "" && !contains(imports, imported.Path) {
						imports = append(imports, imported.Path)
					}
				}
			}
			advisories = append(advisories, Advisory{
				Ecosystem: entries[index].Ecosystem,
				Name:      entries[index].Name,
				Version:   entries[index].Version,
				ID:        id,
				Summary:   detail.Summary,
				Imports:   imports,
			})
		}
	}
	return advisories, nil
}

func queryAdvisoryIDs(ctx context.Context, entries []inventory.Entry, endpoint string) ([][]string, error) {
	ids := make([][]string, len(entries))
	pending := make([]queryWork, 0, len(entries))
	for index, entry := range entries {
		ecosystem, err := osvEcosystem(entry.Ecosystem)
		if err != nil {
			return nil, err
		}
		version := entry.Version
		if ecosystem == "Go" {
			version = strings.TrimPrefix(version, "v")
		}
		pending = append(pending, queryWork{Index: index, Query: osvQuery{
			Package: osvPackage{Ecosystem: ecosystem, Name: entry.Name},
			Version: version,
		}})
	}

	for len(pending) > 0 {
		count := batchSize
		if len(pending) < count {
			count = len(pending)
		}
		batch := pending[:count]
		pending = pending[count:]
		queries := make([]osvQuery, 0, len(batch))
		for _, work := range batch {
			queries = append(queries, work.Query)
		}
		body := struct {
			Queries []osvQuery `json:"queries"`
		}{Queries: queries}
		var response osvBatchResponse
		if err := postJSON(ctx, http.DefaultClient, endpoint, body, &response); err != nil {
			return nil, errs.Wrap(err, "query OSV advisories")
		}
		if len(response.Results) != len(batch) {
			return nil, errs.Msgf("OSV returned %d results for %d queries", len(response.Results), len(batch))
		}
		for index, result := range response.Results {
			work := batch[index]
			for _, vulnerability := range result.Vulnerabilities {
				if vulnerability.ID != "" && !contains(ids[work.Index], vulnerability.ID) {
					ids[work.Index] = append(ids[work.Index], vulnerability.ID)
				}
			}
			if result.NextPageToken != "" {
				work.Query.PageToken = result.NextPageToken
				pending = append(pending, work)
			}
		}
	}
	return ids, nil
}

func osvEcosystem(ecosystem string) (string, error) {
	switch ecosystem {
	case "go":
		return "Go", nil
	case "npm":
		return "npm", nil
	default:
		return "", errs.Msgf("unsupported dependency ecosystem %q", ecosystem)
	}
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}
