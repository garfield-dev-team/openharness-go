package logger_test

import (
	"bytes"
	"testing"

	"github.com/openharness/openharness/pkg/logger"
)

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelWarn)
	lg.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info should be filtered at warn, got %q", buf.String())
	}
	lg.Warn("visible")
	if !bytes.Contains(buf.Bytes(), []byte("visible")) {
		t.Fatalf("warn not emitted, got %q", buf.String())
	}
}

func TestLoggerPrintfBypassesLevel(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelError)
	lg.Printf("raw %s", "output")
	if !bytes.Contains(buf.Bytes(), []byte("raw output")) {
		t.Fatalf("Printf should bypass level, got %q", buf.String())
	}
}

func TestLoggerWithWriterIsReplaceable(t *testing.T) {
	var a, b bytes.Buffer
	lg := logger.New(&a, logger.LevelInfo)
	lg2 := lg.WithWriter(&b)
	lg2.Info("to b")
	if a.Len() != 0 {
		t.Fatalf("original writer should be untouched, got %q", a.String())
	}
	if !bytes.Contains(b.Bytes(), []byte("to b")) {
		t.Fatalf("WithWriter not effective, got %q", b.String())
	}
}

func TestGlobalDefaultReplaceable(t *testing.T) {
	orig := logger.Default()
	defer logger.SetDefault(orig)
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelInfo)
	logger.SetDefault(lg)
	logger.Default().Info("global hello")
	if !bytes.Contains(buf.Bytes(), []byte("global hello")) {
		t.Fatalf("global default not replaced, got %q", buf.String())
	}
}
