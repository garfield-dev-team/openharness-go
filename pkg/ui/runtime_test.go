package ui_test

import (
	"testing"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/session"
	"github.com/openharness/openharness/pkg/types"
	"github.com/openharness/openharness/pkg/ui"
)

func testSettings() *config.Settings {
	return &config.Settings{Model: "test-model", MaxTokens: 64, APIKey: "test-key"}
}

// BuildRuntime must assemble the engine with a durable session store and
// seed conversation history from the resumed session's active branch.
// Removing either wiring breaks this test.
func TestBuildRuntimeWiresSessionStoreAndResume(t *testing.T) {
	cwd := t.TempDir()

	rt, err := ui.BuildRuntime(testSettings(), cwd)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()
	if rt.Store == nil {
		t.Fatal("runtime assembled without a session store")
	}
	if session.FilePath(cwd, rt.SessionID) == "" {
		t.Fatal("session id missing")
	}

	// Seed a prior session on disk, then resume it.
	prior, err := session.Open(session.FilePath(cwd, "session_prior"))
	if err != nil {
		t.Fatalf("seed prior session: %v", err)
	}
	if _, err := prior.AppendMessage(types.FromUserText("resumed history marker")); err != nil {
		t.Fatalf("append seed: %v", err)
	}
	prior.Close()

	resumed, err := ui.BuildRuntime(testSettings(), cwd, ui.WithResumeSession("session_prior"))
	if err != nil {
		t.Fatalf("build resumed runtime: %v", err)
	}
	defer resumed.Close()

	if resumed.SessionID != "session_prior" {
		t.Fatalf("expected resumed session id, got %q", resumed.SessionID)
	}
	if resumed.Engine.CurrentTokens() == 0 {
		t.Fatal("resumed engine did not load history from the session store")
	}
	if got := len(resumed.Store.ActiveMessages()); got != 1 {
		t.Fatalf("store active path should hold the seeded message, got %d", got)
	}
}
