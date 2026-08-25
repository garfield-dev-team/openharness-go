// Package logger provides a minimal, replaceable logging abstraction for
// terminal and diagnostic output.
//
// It wraps log/slog so callers depend on the Logger interface, not on fmt or
// log directly. Swapping the implementation (zap, logrus, etc.) only requires
// implementing Logger or calling SetDefault with a new instance.
//
// Usage:
//
//	log := logger.New(os.Stderr, logger.LevelInfo)
//	log.Info("hello", "model", "claude")
//	log.Printf("plain: %s", msg) // level-unfiltered, for streaming output
package logger

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// Level controls verbosity. Lower values are more verbose.
type Level int

const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// Logger is the replaceable logging contract.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	Printf(format string, args ...any)
	Println(args ...any)
	Print(msg string)
	Writer() io.Writer
	WithWriter(w io.Writer) Logger
	WithLevel(l Level) Logger
	Level() Level
	Enabled(l Level) bool
}

// slogLogger is the default implementation backed by log/slog.
type slogLogger struct {
	level Level
	out   io.Writer
	inner *slog.Logger
}

func newSlogLogger(out io.Writer, level Level) *slogLogger {
	if out == nil {
		out = io.Discard
	}
	h := slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.Level(level)})
	return &slogLogger{level: level, out: out, inner: slog.New(h)}
}

// New creates a Logger writing to out at the given level.
func New(out io.Writer, level Level) Logger {
	return newSlogLogger(out, level)
}

// NewNop returns a Logger that discards all output.
func NewNop() Logger {
	return newSlogLogger(io.Discard, LevelError+10)
}

// NewBuffer returns a Logger that writes to an in-memory buffer and the
// buffer itself for assertions in tests.
func NewBuffer(level Level) (Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return newSlogLogger(&buf, level), &buf
}

func (l *slogLogger) Level() Level { return l.level }

func (l *slogLogger) Enabled(lv Level) bool { return lv >= l.level }

func (l *slogLogger) Debug(msg string, args ...any) {
	if l.Enabled(LevelDebug) {
		l.inner.Debug(msg, args...)
	}
}

func (l *slogLogger) Info(msg string, args ...any) {
	if l.Enabled(LevelInfo) {
		l.inner.Info(msg, args...)
	}
}

func (l *slogLogger) Warn(msg string, args ...any) {
	if l.Enabled(LevelWarn) {
		l.inner.Warn(msg, args...)
	}
}

func (l *slogLogger) Error(msg string, args ...any) {
	if l.Enabled(LevelError) {
		l.inner.Error(msg, args...)
	}
}

// Printf writes formatted output directly to the underlying writer,
// bypassing level filtering (for streaming/terminal rendering).
func (l *slogLogger) Printf(format string, args ...any) {
	fmt.Fprintf(l.out, format, args...)
}

// Println writes args with spaces and a trailing newline.
func (l *slogLogger) Println(args ...any) {
	fmt.Fprintln(l.out, args...)
}

// Print writes a raw string without newline.
func (l *slogLogger) Print(msg string) {
	fmt.Fprint(l.out, msg)
}

func (l *slogLogger) Writer() io.Writer { return l.out }

func (l *slogLogger) WithWriter(w io.Writer) Logger {
	return newSlogLogger(w, l.level)
}

func (l *slogLogger) WithLevel(level Level) Logger {
	return newSlogLogger(l.out, level)
}

// ---------------------------------------------------------------------------
// Global default (for diagnostic code that doesn't have a Logger injected)
// ---------------------------------------------------------------------------

var (
	globalMu sync.RWMutex
	global   Logger = newSlogLogger(os.Stderr, LevelInfo)
)

// Default returns the global Logger.
func Default() Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// SetDefault replaces the global Logger.
func SetDefault(l Logger) {
	if l == nil {
		return
	}
	globalMu.Lock()
	global = l
	globalMu.Unlock()
}

// LevelFromVerbose maps Settings.Verbose to a Level.
func LevelFromVerbose(verbose bool) Level {
	if verbose {
		return LevelDebug
	}
	return LevelInfo
}
