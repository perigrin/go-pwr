// ABOUTME: Tests that bytes discarded as an earlier reply's tail during a
// ABOUTME: successful read are logged at debug level, which stays quiet by default.

package ble

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
)

// awaitLog reads a status link fed chunks, and returns what the read gave and
// what it logged at level.
func awaitLog(t *testing.T, level slog.Level, chunks [][]byte) (string, string) {
	t.Helper()
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	l := &statusLink{buf: newReadBuffer(nil), wake: make(chan struct{}, 1)}
	l.buf.begin()
	if _, _, err := l.buf.bound(0); err != nil {
		t.Fatal(err)
	}
	for i, c := range chunks {
		l.notified(uint64(i+1), c)
	}
	reply, err := l.await(context.Background(), make(chan struct{}), status.NewCompletion(time.Now(), time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	return reply, logged.String()
}

func TestSuccessfulReadLogsDiscardedBytes(t *testing.T) {
	// A real 20-byte notification from the middle of another real reply,
	// then a real reply.
	chunks := append([][]byte{lolaChunks(t, 2)[4]}, lolaChunks(t, 1)...)
	reply, logged := awaitLog(t, slog.LevelDebug, chunks)
	if len(reply) != 169 {
		t.Fatalf("reply is %d bytes, want 169", len(reply))
	}
	if !strings.Contains(logged, "level=DEBUG") || !strings.Contains(logged, "discarded=20") {
		t.Errorf("log %q, want a debug line with discarded=20", logged)
	}
}

func TestSuccessfulReadWithoutDiscardsLogsNothing(t *testing.T) {
	_, logged := awaitLog(t, slog.LevelDebug, lolaChunks(t, 1))
	if logged != "" {
		t.Errorf("log %q, want nothing when nothing was discarded", logged)
	}
}

func TestDiscardedBytesAreQuietAtDefaultLevel(t *testing.T) {
	chunks := append([][]byte{lolaChunks(t, 2)[4]}, lolaChunks(t, 1)...)
	_, logged := awaitLog(t, slog.LevelInfo, chunks)
	if logged != "" {
		t.Errorf("log %q at info level, want nothing", logged)
	}
}
