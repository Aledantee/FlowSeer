package restconf

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// discovery.go implements RFC 8040 §3.1 API-root discovery: the
// /.well-known/host-meta XRD document names the root via a Link with
// rel="restconf"; peers without host-meta are probed at the
// conventional /restconf root.

// xrd is the RFC 6415 host-meta document, reduced to the links.
type xrd struct {
	XMLName xml.Name  `xml:"XRD"`
	Links   []xrdLink `xml:"Link"`
}

type xrdLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// discoverRoot resolves the peer's RESTCONF root path.
func discoverRoot(ctx context.Context, client *http.Client, base string, auth func(*http.Request)) (string, error) {
	root, err := hostMetaRoot(ctx, client, base, auth)
	if err == nil {
		return root, nil
	}

	// Fallback: probe the conventional root. yang-library-version is
	// tiny and mandatory (RFC 8040 §3.3.3).
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/restconf/yang-library-version", nil)
	if rerr != nil {
		return "", errs.From(rerr).Code(ErrCodeDiscovery).Msg("build probe request")
	}
	req.Header.Set("Accept", yangDataJSON)
	auth(req)
	resp, perr := client.Do(req)
	if perr != nil {
		return "", errs.From(perr).Code(ErrCodeDiscovery).Msgf("no host-meta (%v) and the /restconf probe did not answer", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "/restconf", nil
	}
	return "", errs.New().
		Code(ErrCodeDiscovery).
		Attr("probe_status", resp.StatusCode).
		Msgf("no host-meta (%v) and the /restconf probe answered HTTP %d", err, resp.StatusCode)
}

// hostMetaRoot reads /.well-known/host-meta and extracts the restconf
// link.
func hostMetaRoot(ctx context.Context, client *http.Client, base string, auth func(*http.Request)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/host-meta", nil)
	if err != nil {
		return "", errs.From(err).Code(ErrCodeDiscovery).Msg("build host-meta request")
	}
	req.Header.Set("Accept", "application/xrd+xml")
	auth(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", errs.From(err).Code(ErrCodeDiscovery).Msg("fetch host-meta")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", errs.New().Code(ErrCodeDiscovery).Attr("status", resp.StatusCode).Msg("host-meta not available")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", errs.From(err).Code(ErrCodeDiscovery).Msg("read host-meta")
	}
	var doc xrd
	if err := xml.Unmarshal(body, &doc); err != nil {
		return "", errs.From(err).Code(ErrCodeDiscovery).Msg("parse host-meta XRD")
	}
	for _, link := range doc.Links {
		if link.Rel == "restconf" && link.Href != "" {
			return strings.TrimSuffix(link.Href, "/"), nil
		}
	}
	return "", errs.New().Code(ErrCodeDiscovery).Msg("host-meta lists no restconf link")
}
