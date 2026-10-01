package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/module"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/tools/deps/inventory"
)

const releaseWait = 14 * 24 * time.Hour

// Publication is a dependency's registry publication date and optional Go
// proxy origin commit, with the 14-day release status evaluated at Now.
type Publication struct {
	Ecosystem     string    `json:"ecosystem"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	Published     time.Time `json:"published"`
	Under14Days   bool      `json:"under_14_days"`
	Until         time.Time `json:"until"`
	OriginCommit  string    `json:"origin_commit,omitempty"`
	OriginMissing bool      `json:"origin_missing"`
}

type goInfo struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
	Origin  *goOrigin `json:"Origin"`
}

type goOrigin struct {
	Hash string `json:"Hash"`
}

type npmDocument struct {
	Time map[string]time.Time `json:"time"`
}

// Age fetches one publication record per inventory entry. Go records come
// from the module proxy and npm records come from the npm registry.
func Age(ctx context.Context, entries []inventory.Entry, goProxyBase, npmRegistryBase string, now time.Time) ([]Publication, error) {
	publications := make([]Publication, 0, len(entries))
	for _, entry := range entries {
		var published time.Time
		var origin string
		originMissing := false
		switch entry.Ecosystem {
		case "go":
			info, err := fetchGoInfo(ctx, entry, goProxyBase)
			if err != nil {
				return nil, err
			}
			published = info.Time
			if info.Origin == nil {
				originMissing = true
			} else {
				origin = info.Origin.Hash
			}
		case "npm":
			var document npmDocument
			endpoint := strings.TrimRight(npmRegistryBase, "/") + "/" + url.PathEscape(entry.Name)
			if err := getJSON(ctx, http.DefaultClient, endpoint, &document); err != nil {
				return nil, errs.Wrapf(err, "fetch npm publication for %s", entry.Name)
			}
			var ok bool
			published, ok = document.Time[entry.Version]
			if !ok {
				return nil, errs.Msgf("npm registry has no publication date for %s@%s", entry.Name, entry.Version)
			}
		default:
			return nil, errs.Msgf("unsupported dependency ecosystem %q", entry.Ecosystem)
		}
		until := published.Add(releaseWait)
		publications = append(publications, Publication{
			Ecosystem:     entry.Ecosystem,
			Name:          entry.Name,
			Version:       entry.Version,
			Published:     published,
			Under14Days:   now.Before(until),
			Until:         until,
			OriginCommit:  origin,
			OriginMissing: originMissing,
		})
	}
	return publications, nil
}

func fetchGoInfo(ctx context.Context, entry inventory.Entry, baseURL string) (goInfo, error) {
	escaped, err := module.EscapePath(entry.Name)
	if err != nil {
		return goInfo{}, errs.Wrapf(err, "escape Go module path %s", entry.Name)
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + escaped + "/@v/" + url.PathEscape(entry.Version) + ".info"
	var info goInfo
	if err := getJSON(ctx, http.DefaultClient, endpoint, &info); err != nil {
		return goInfo{}, errs.Wrapf(err, "fetch Go publication for %s", entry.Name)
	}
	return info, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errs.Wrap(err, "create lookup request")
	}
	response, err := client.Do(request)
	if err != nil {
		return errs.Wrap(err, "send lookup request")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errs.Msgf("lookup request returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return errs.Wrap(err, "decode lookup response")
	}
	return nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return errs.Wrap(err, "encode lookup request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return errs.Wrap(err, "create lookup request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errs.Wrap(err, "send lookup request")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errs.Msgf("lookup request returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return errs.Wrap(err, "decode lookup response")
	}
	return nil
}
