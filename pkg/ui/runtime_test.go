package ui_test

import (
	"bytes"
	"testing"

	"github.com/openharness/openharness/pkg/config"
	"github.com/openharness/openharness/pkg/logger"
	"github.com/openharness/openharness/pkg/services"
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

// BuildRuntime must wire a model-aware compaction threshold and an
// injectable logger; removing either breaks these tests.
func TestBuildRuntimeWiresCompactionThresholdAndLogger(t *testing.T) {
	cwd := t.TempDir()

	// claude-opus-5 -> 1M * 0.8 = 800k
	claude := &config.Settings{Model: "claude-opus-5", MaxTokens: 64, APIKey: "k"}
	rtClaude, err := ui.BuildRuntime(claude, cwd)
	if err != nil {
		t.Fatalf("build claude runtime: %v", err)
	}
	defer rtClaude.Close()
	if got := rtClaude.Engine.CompactionThreshold(); got != int(float64(1_000_000)*services.DefaultCompactionRatio) {
		t.Fatalf("claude threshold = %d", got)
	}

	// muse-spark is the free OpenCode tier — must not require an API key
	free := &config.Settings{Model: "muse-spark-1.2-contributor-free", MaxTokens: 64, Provider: "opencode", BaseURL: strPtr("https://opencode.ai/zen/v1")}
	rtFree, err := ui.BuildRuntime(free, cwd)
	if err != nil {
		t.Fatalf("build opencode runtime: %v", err)
	}
	defer rtFree.Close()
	if got, want := rtFree.Engine.CompactionThreshold(), services.ThresholdForModel("muse-spark"); got != want {
		t.Fatalf("muse-spark threshold = %d want %d", got, want)
	}

	// gpt-5.6-sol -> 1_050_000 * 0.8 = 840k
	gpt := &config.Settings{Model: "gpt-5.6-sol", MaxTokens: 64, APIKey: "k"}
	rtGPT, err := ui.BuildRuntime(gpt, cwd)
	if err != nil {
		t.Fatalf("build gpt runtime: %v", err)
	}
	defer rtGPT.Close()
	if got := rtGPT.Engine.CompactionThreshold(); got != int(float64(1_050_000)*services.DefaultCompactionRatio) {
		t.Fatalf("gpt-5.6-sol threshold = %d", got)
	}

	// Explicit override via settings + logger injection
	var buf bytes.Buffer
	lg := logger.New(&buf, logger.LevelInfo)
	over := &config.Settings{Model: "gpt-5.6-sol", MaxTokens: 64, APIKey: "k", CompactionThreshold: 42_000}
	rtOver, err := ui.BuildRuntime(over, cwd, ui.WithLogger(lg))
	if err != nil {
		t.Fatalf("build overridden runtime: %v", err)
	}
	defer rtOver.Close()
	if got := rtOver.Engine.CompactionThreshold(); got != 42_000 {
		t.Fatalf("override threshold = %d want 42000", got)
	}
	if rtOver.Logger == nil {
		t.Fatal("logger not wired")
	}
	// Logger must be replaceable: HandleLine would write to buf via rtOver.Logger
	rtOver.Logger.Info("wiring-probe")
	if !bytes.Contains(buf.Bytes(), []byte("wiring-probe")) {
		t.Fatalf("injected logger not used, got %q", buf.String())
	}
}

func TestSwitchModelUpdatesEngineAndState(t *testing.T) {
	cwd := t.TempDir()
	s := &config.Settings{Model: "claude-opus-5", MaxTokens: 64, APIKey: "k"}
	rt, err := ui.BuildRuntime(s, cwd)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.SwitchModel("muse-spark-1.2-contributor-free", false); err != nil {
		t.Fatalf("switch to muse-spark: %v", err)
	}
	if rt.Settings.Model != "muse-spark-1.2-contributor-free" {
		t.Fatalf("settings model not updated: %q", rt.Settings.Model)
	}
	if rt.Settings.Provider != "opencode" {
		t.Fatalf("provider not auto-switched to opencode: %q", rt.Settings.Provider)
	}
	if got, want := rt.Engine.CompactionThreshold(), services.ThresholdForModel("muse-spark"); got != want {
		t.Fatalf("threshold after switch = %d want %d", got, want)
	}
	if st := rt.AppState.Get(); st.Model != "muse-spark-1.2-contributor-free" || st.Provider != "opencode" {
		t.Fatalf("app state not updated: %+v", st)
	}
	// Switching back to claude clears opencode wiring.
	if err := rt.SwitchModel("claude-opus-5", false); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	if rt.Settings.Provider == "opencode" {
		t.Fatalf("provider should be cleared when leaving opencode")
	}
}

func TestListKnownModelsHasStablePickerOrder(t *testing.T) {
	models := services.ListKnownModels()
	if len(models) < 13 {
		t.Fatalf("expected >=13 curated models, got %d", len(models))
	}
	// First entry is the current default picker head — must be deterministic for provider cache.
	if models[0].ID != "claude-opus-5" {
		t.Fatalf("unexpected first model: %q", models[0].ID)
	}
	// muse-spark must be present (free tier) and selectable without typo.
	found := false
	for _, m := range models {
		if m.ID == "muse-spark-1.2-contributor-free" || m.ID == "muse-spark" {
			found = true
		}
	}
	// We expose the prefix "muse-spark" as the catalogue key.
	found = false
	for _, m := range models {
		if m.ID == "muse-spark" {
			found = true
		}
	}
	if !found {
		t.Fatalf("muse-spark not in picker list")
	}
}

func strPtr(s string) *string { return &s }
