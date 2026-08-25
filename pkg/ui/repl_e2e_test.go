package ui_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/logger"
	"github.com/openharness/openharness/pkg/ui"
)

// sseOpenAIServer returns an OpenAI-compatible server that replies with the
// given content chunks for every chat completion request.
func sseOpenAIServer(t *testing.T, contentChunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, c := range contentChunks {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":" + quote(c) + "}}]}\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// HandleLine must render streamed assistant text for a plain reply.
func TestHandleLineRendersAssistantText(t *testing.T) {
	srv := sseOpenAIServer(t, []string{"Hello", " there"})
	defer srv.Close()

	baseURL := srv.URL
	settings := &config.Settings{
		APIKey:    "test",
		Model:     "fake-model",
		Provider:  "openai-compatible",
		BaseURL:   &baseURL,
		MaxTokens: 64,
	}

	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelInfo)
	rt, err := ui.BuildRuntime(settings, t.TempDir(), ui.WithLogger(lg))
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := rt.HandleLine(context.Background(), "hi"); err != nil {
		t.Errorf("handle line: %v", err)
	}
	if !strings.Contains(buf.String(), "Hello there") {
		t.Fatalf("assistant text not rendered, got: %q", buf.String())
	}
}

// Reasoning models may stream only reasoning_content with empty final
// content. The REPL must still surface something visible, never silence.
func TestHandleLineReasoningOnlyReplyIsVisible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking hard\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	baseURL := srv.URL
	settings := &config.Settings{
		APIKey:    "test",
		Model:     "reasoner",
		Provider:  "openai-compatible",
		BaseURL:   &baseURL,
		MaxTokens: 64,
	}
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelInfo)
	rt, err := ui.BuildRuntime(settings, t.TempDir(), ui.WithLogger(lg))
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := rt.HandleLine(context.Background(), "hi"); err != nil {
		t.Errorf("handle line: %v", err)
	}
	if !strings.Contains(ansiStrip(buf.String()), "thinking hard") {
		t.Fatalf("reasoning content not rendered, got: %q", buf.String())
	}
}

// ansiStrip removes ANSI escape sequences so assertions check visible text.
func ansiStrip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !isTerminator(s[j]) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isTerminator(c byte) bool { return c >= 0x40 && c <= 0x7e }

// OpenRouter-style reasoning models stream delta.reasoning (not
// reasoning_content). The reply must still be visible.
func TestHandleLineOpenRouterReasoningFieldIsVisible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"pondering\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"final answer\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	baseURL := srv.URL
	settings := &config.Settings{
		APIKey:    "test",
		Model:     "openrouter-reasoner",
		Provider:  "openai-compatible",
		BaseURL:   &baseURL,
		MaxTokens: 64,
	}
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelInfo)
	rt, err := ui.BuildRuntime(settings, t.TempDir(), ui.WithLogger(lg))
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := rt.HandleLine(context.Background(), "hi"); err != nil {
		t.Errorf("handle line: %v", err)
	}
	visible := ansiStrip(buf.String())
	if !strings.Contains(visible, "pondering") {
		t.Fatalf("delta.reasoning content not rendered, got: %q", visible)
	}
	if !strings.Contains(visible, "final answer") {
		t.Fatalf("content not rendered, got: %q", visible)
	}
}

// API-level errors must be surfaced as an error, not swallowed.
func TestHandleLineSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	baseURL := srv.URL
	settings := &config.Settings{
		APIKey:    "test",
		Model:     "missing-model",
		Provider:  "openai-compatible",
		BaseURL:   &baseURL,
		MaxTokens: 64,
	}
	rt, err := ui.BuildRuntime(settings, t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	err = rt.HandleLine(context.Background(), "hi")
	if err == nil {
		t.Fatal("API error was swallowed — HandleLine returned nil")
	}
}
