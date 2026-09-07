package telemetry_test

import (
	"bytes"
	"log/slog"
)

func slogTo(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
