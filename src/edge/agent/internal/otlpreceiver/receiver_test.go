package otlpreceiver_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/otlpreceiver"
)

type appendRecorder struct {
	mu      sync.Mutex
	records [][]byte
	err     error
}

func (a *appendRecorder) Append(record []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, bytes.Clone(record))
	return a.err
}

func TestReceiver(t *testing.T) {
	body := []byte{0x0a, 0x00}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name        string
		path        string
		body        []byte
		contentType string
		encoding    string
		appendErr   error
		status      int
		signal      telemetryv1.TelemetrySignal
		appends     int
	}{
		{name: "logs", path: "logs", body: body, status: 200, signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, appends: 1},
		{name: "identity", path: "logs", body: body, encoding: "identity", status: 200, signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, appends: 1},
		{name: "metrics", path: "metrics", body: body, status: 200, signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, appends: 1},
		{name: "traces", path: "traces", body: body, status: 200, signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_TRACES, appends: 1},
		{name: "gzip", path: "logs", body: compressed.Bytes(), encoding: "gzip", status: 415},
		{name: "JSON", path: "logs", body: []byte(`{}`), contentType: "application/json", status: 415},
		{name: "over_bound", path: "logs", body: make([]byte, 261121), status: 413},
		{name: "at_bound", path: "logs", body: make([]byte, 261120), status: 200, signal: telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, appends: 1},
		{name: "append_failure", path: "logs", body: body, appendErr: errors.New("disk unavailable"), status: 503, appends: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			appender := &appendRecorder{err: tt.appendErr}
			r, err := otlpreceiver.Start(appender)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := r.Close(ctx); err != nil {
					t.Errorf("Close() = %v", err)
				}
			})
			endpoint, err := url.Parse(r.Endpoint())
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.Port() == "0" {
				t.Fatalf("Endpoint() = %q, want bound loopback HTTP URL", r.Endpoint())
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.Endpoint()+"/v1/"+tt.path, bytes.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			contentType := tt.contentType
			if contentType == "" {
				contentType = "application/x-protobuf"
			}
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("Content-Encoding", tt.encoding)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			responseBody, readErr := io.ReadAll(resp.Body)
			closeErr := resp.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read response = %v, close response = %v", readErr, closeErr)
			}
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status == http.StatusOK && (resp.Header.Get("Content-Type") != "application/x-protobuf" || len(responseBody) != 0) {
				t.Fatalf("response type = %q, body = %x, want empty protobuf", resp.Header.Get("Content-Type"), responseBody)
			}
			appender.mu.Lock()
			defer appender.mu.Unlock()
			if len(appender.records) != tt.appends {
				t.Fatalf("appends = %d, want %d", len(appender.records), tt.appends)
			}
			if tt.status == http.StatusOK {
				var export telemetryv1.TelemetryExport
				if err := proto.Unmarshal(appender.records[0], &export); err != nil {
					t.Fatal(err)
				}
				if export.GetSignal() != tt.signal || !bytes.Equal(export.GetBody(), tt.body) {
					t.Fatalf("export signal = %v, body length = %d, want signal %v and identical %d bytes", export.GetSignal(), len(export.GetBody()), tt.signal, len(tt.body))
				}
				if tt.name == "at_bound" && len(appender.records[0]) != 261126 {
					t.Errorf("record length = %d, want 261126", len(appender.records[0]))
				}
			}
			if err := r.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			conn, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
			if err == nil {
				if closeErr := conn.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("receiver still accepts connections after Close")
			}
		})
	}
}
