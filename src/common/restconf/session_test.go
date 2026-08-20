package restconf_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/restconf"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// dialTest wires a Session against an httptest server.
func dialTest(t *testing.T, handler http.Handler) *restconf.Session {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s, err := restconf.Dial(context.Background(), srv.URL, restconf.Options{
		Username:   "admin",
		Password:   "secret",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// hostMetaHandler serves an XRD document rooting RESTCONF at root.
func hostMetaHandler(root string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/host-meta" {
			w.Header().Set("Content-Type", "application/xrd+xml")
			_, _ = w.Write([]byte(`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">` +
				`<Link rel="restconf" href="` + root + `"/></XRD>`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestDiscoveryHostMeta(t *testing.T) {
	s := dialTest(t, hostMetaHandler("/custom-root", http.NotFoundHandler()))
	if s.Root() != "/custom-root" {
		t.Errorf("Root() = %q, want /custom-root", s.Root())
	}
}

func TestDiscoveryFallbackProbe(t *testing.T) {
	s := dialTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/restconf/yang-library-version" {
			w.Header().Set("Content-Type", "application/yang-data+json")
			_, _ = w.Write([]byte(`{"yang-library-version":"2016-06-21"}`))
			return
		}
		http.NotFound(w, r)
	}))
	if s.Root() != "/restconf" {
		t.Errorf("Root() = %q, want /restconf", s.Root())
	}
}

func TestDiscoveryNeitherFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := restconf.Dial(context.Background(), srv.URL, restconf.Options{HTTPClient: srv.Client()})
	if code, ok := errs.CodeOf(err); !ok || code != restconf.ErrCodeDiscovery {
		t.Fatalf("Dial without any root = %v, want %v", err, restconf.ErrCodeDiscovery)
	}
}

// serverPath is the shared fixture path with characters that must
// percent-encode.
func serverPath() yang.Path {
	return yang.Path{Segments: []yang.Segment{
		{Module: "fixture-main", Name: "servers"},
		{Name: "server", Keys: []yang.KeyValue{{Name: "name", Value: "edge 1/0:a"}}},
	}}
}

func TestGetEncodesURIAndAuth(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotAccept string
	s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"fixture-main:server":[{"name":"edge 1/0:a"}]}`))
	})))

	body, err := s.Get(context.Background(), serverPath(), restconf.GetOptions{Depth: 2, Fields: "name"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := "/restconf/data/fixture-main:servers/server=edge%201%2F0%3Aa"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotQuery != "depth=2&fields=name" {
		t.Errorf("query = %q", gotQuery)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("no basic auth header: %q", gotAuth)
	}
	if gotAccept != "application/yang-data+json" {
		t.Errorf("accept = %q", gotAccept)
	}
	if !strings.Contains(string(body), "edge 1/0:a") {
		t.Errorf("body = %s", body)
	}
}

func TestPutWithETagStaleConflict(t *testing.T) {
	s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write([]byte(`{}`))
		case http.MethodPut:
			if r.Header.Get("If-Match") != `"current"` {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"ietf-restconf:errors":{"error":[{"error-type":"protocol","error-tag":"operation-failed","error-message":"stale etag"}]}}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})))

	_, err := s.Put(context.Background(), serverPath(), []byte(`{"name":"x"}`))
	if code, ok := errs.CodeOf(err); !ok || code != restconf.ErrCodeConflict {
		t.Fatalf("stale write = %v, want %v", err, restconf.ErrCodeConflict)
	}
	if !errs.Retryable(err) {
		t.Error("conflict not marked retryable")
	}
}

func TestPutWithoutETagDegradesToUnconditional(t *testing.T) {
	var sawIfMatch bool
	var readBacks int
	s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			readBacks++
			_, _ = w.Write([]byte(`{"fixture-main:server":[{"name":"edge 1/0:a","port":8080}]}`))
		case http.MethodPut:
			sawIfMatch = r.Header.Get("If-Match") != ""
			w.WriteHeader(http.StatusNoContent)
		}
	})))

	res, err := s.Put(context.Background(), serverPath(), []byte(`{"port":8080}`))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if res.UsedIfMatch || sawIfMatch {
		t.Error("If-Match sent although the peer offered no ETag")
	}
	if !strings.Contains(string(res.ReadBack), `"port":8080`) {
		t.Errorf("read-back = %s", res.ReadBack)
	}
	if readBacks < 2 { // ETag capture GET + verification GET
		t.Errorf("expected capture and read-back GETs, saw %d", readBacks)
	}
}

func TestErrorDecodeConformantAndMalformed(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantAttr string
		wantMsg  string
	}{
		{
			name:     "conformant",
			body:     `{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"invalid-value","error-app-tag":"range","error-path":"/servers","error-message":"port out of range"}]}}`,
			wantAttr: "invalid-value",
			wantMsg:  "port out of range",
		},
		{
			name:     "malformed body preserved raw",
			body:     `<html>ICX error page</html>`,
			wantAttr: "<html>ICX error page</html>",
			wantMsg:  "nonconformant error body",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			})))
			_, err := s.Get(context.Background(), serverPath(), restconf.GetOptions{})
			if err == nil {
				t.Fatal("Get succeeded on a 400")
			}
			if code, ok := errs.CodeOf(err); !ok || code != restconf.ErrCodeDevice {
				t.Errorf("code = %v, want %v", code, restconf.ErrCodeDevice)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("message %q missing %q", err.Error(), tc.wantMsg)
			}
			attrs := errs.Attributes(err)
			found := false
			for _, v := range attrs {
				if sv, ok := v.(string); ok && strings.Contains(sv, tc.wantAttr) {
					found = true
				}
			}
			if !found {
				t.Errorf("attributes %v missing %q", attrs, tc.wantAttr)
			}
		})
	}
}

func TestGetAbsentResourceIsNil(t *testing.T) {
	s := dialTest(t, hostMetaHandler("/restconf", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "{}", http.StatusNotFound)
	})))
	body, err := s.Get(context.Background(), serverPath(), restconf.GetOptions{})
	if err != nil || body != nil {
		t.Fatalf("Get(absent) = (%s, %v), want (nil, nil)", body, err)
	}
}

func TestInsecureTLSRequiresExplicitOptIn(t *testing.T) {
	srv := httptest.NewTLSServer(hostMetaHandler("/restconf", http.NotFoundHandler()))
	defer srv.Close()

	// Default posture: the self-signed peer is rejected.
	_, err := restconf.Dial(context.Background(), srv.URL, restconf.Options{})
	if err == nil {
		t.Fatal("Dial accepted an unverifiable TLS peer without the opt-in")
	}

	// Explicit opt-in connects.
	s, err := restconf.Dial(context.Background(), srv.URL, restconf.Options{InsecureSkipTLSVerify: true})
	if err != nil {
		t.Fatalf("Dial with explicit insecure opt-in: %v", err)
	}
	_ = s.Close()
}
