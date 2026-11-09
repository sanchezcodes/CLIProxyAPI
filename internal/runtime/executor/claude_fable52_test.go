package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestClaudeFableOrMythos52Model_DigitBoundary(t *testing.T) {
	for _, m := range []string{
		"claude-fable-5-2",
		"claude-fable-5.2",
		"claude-fable-5-2[1m]",
		"claude-fable-5-2-20261109",
		"anthropic/claude-fable-5-2",
		"claude-mythos-5-2",
		"claude-mythos-5.2",
	} {
		if !isClaudeFableOrMythos52Model(m) {
			t.Errorf("isClaudeFableOrMythos52Model(%q) = false, want true", m)
		}
	}
	for _, m := range []string{
		"claude-fable-5-20",
		"claude-mythos-5-20",
		"claude-fable-5-1",
		"claude-fable-5",
		"claude-opus-5-5",
	} {
		if isClaudeFableOrMythos52Model(m) {
			t.Errorf("isClaudeFableOrMythos52Model(%q) = true, want false", m)
		}
	}
	if isClaudeFable52Model("claude-mythos-5-2") {
		t.Error("isClaudeFable52Model must not match Mythos 5.2")
	}
}

func TestClaudeModelCapabilities_FableAndMythos52Generation(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2", "claude-fable-5-2[1m]"} {
		if !claudeModelHasPerTurnEffort(model) {
			t.Errorf("claudeModelHasPerTurnEffort(%q) = false, want true", model)
		}
		if !claudeModelHasPerTurnTiming(model) {
			t.Errorf("claudeModelHasPerTurnTiming(%q) = false, want true", model)
		}
		if !claudeModelUsesProgressDisplay(model) {
			t.Errorf("claudeModelUsesProgressDisplay(%q) = false, want true", model)
		}
	}
	for _, model := range []string{
		"claude-fable-5-1", "claude-fable-5-2", "claude-mythos-5-1", "claude-mythos-5-2",
		"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5",
	} {
		if !isClaudePreservedThinkingModel(model) {
			t.Errorf("isClaudePreservedThinkingModel(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-5"} {
		if isClaudePreservedThinkingModel(model) {
			t.Errorf("isClaudePreservedThinkingModel(%q) = true, want false", model)
		}
	}
}

func TestClaudeCloakFallbackModel(t *testing.T) {
	for model, want := range map[string]string{
		"claude-fable-5-1":  "claude-opus-5",
		"claude-fable-5-2":  "claude-opus-5",
		"claude-mythos-5-1": "claude-opus-5",
		"claude-mythos-5-2": "",
		"claude-opus-5-5":   "claude-opus-4-8",
		"claude-fable-5":    "",
		"claude-sonnet-5-5": "",
	} {
		if got := claudeCloakFallbackModel(model); got != want {
			t.Errorf("claudeCloakFallbackModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestClaudeExecutor_Cloaked52GenerationMatchesClaudeCode(t *testing.T) {
	for _, tc := range []struct {
		model        string
		wantFallback string
	}{
		{model: "claude-fable-5-2", wantFallback: "claude-opus-5"},
		{model: "claude-mythos-5-2"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var seenBody []byte
			var seenHeaders http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenBody, _ = io.ReadAll(r.Body)
				seenHeaders = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"` + tc.model + `","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer server.Close()

			auth := &cliproxyauth.Auth{ID: "cloaked-" + tc.model, Metadata: claudeOAuthTestMetadata(), Attributes: map[string]string{
				"api_key": "sk-ant-oat-cloaked-52", "base_url": server.URL,
			}}
			payload := []byte(`{"model":"` + tc.model + `","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"test"}]}`)
			executor := NewClaudeExecutor(&config.Config{})
			_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
				Model: tc.model, Payload: payload,
			}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude})
			if err != nil {
				t.Fatal(err)
			}

			if got := ojson.GetBytes(seenBody, "fallbacks.0.model").String(); got != tc.wantFallback {
				t.Errorf("fallback model = %q, want %q; body=%s", got, tc.wantFallback, seenBody)
			}
			if got := ojson.GetBytes(seenBody, "thinking.display").String(); got != "updates" {
				t.Errorf("thinking.display = %q, want updates", got)
			}
			// Neither model carries fable_5_mitigations in the Claude Code 2.1.321 catalog.
			if strings.Contains(strings.ReplaceAll(ojson.GetBytes(seenBody, "system").Raw, "\u200b", ""), "Reporting outcomes") {
				t.Errorf("5.2 generation must not carry the Reporting outcomes block: %s", seenBody)
			}
			betas := seenHeaders.Get("Anthropic-Beta")
			for _, beta := range []string{claudePerTurnControlBeta, claudeThinkingBindingBeta, claudeThinkingDisplayUpdatesBeta} {
				if !strings.Contains(betas, beta) {
					t.Errorf("Anthropic-Beta missing %s: %s", beta, betas)
				}
			}
			if hasFallbackBeta := strings.Contains(betas, claudeServerSideFallbackBeta); hasFallbackBeta != (tc.wantFallback != "") {
				t.Errorf("server-side fallback beta present = %v, want %v: %s", hasFallbackBeta, tc.wantFallback != "", betas)
			}
		})
	}
}

func TestClaudeFable52FallbackReconcilesAfterModelOverride(t *testing.T) {
	before := []byte(`{"model":"claude-fable-5-1","system":[{"type":"text","text":"billing"}]}`)
	cloaked := []byte(`{"model":"claude-fable-5-1","fallbacks":[{"model":"claude-opus-5"}],"system":[{"type":"text","text":"billing"},{"type":"text","text":` + quoteJSON(claudeCodeFableReportingOutcomes) + `}]}`)
	state := captureClaudeCodeFableState(before, cloaked, true)

	rewritten := []byte(strings.Replace(string(cloaked), `"model":"claude-fable-5-1"`, `"model":"claude-fable-5-2"`, 1))
	got := reconcileClaudeCodeFableModelAfterPayload(rewritten, state, false, false, true, false)
	if fallback := ojson.GetBytes(got, "fallbacks.0.model").String(); fallback != "claude-opus-5" {
		t.Errorf("Fable 5.1 rewritten to 5.2 fallback = %q, want claude-opus-5", fallback)
	}
	if hasFableReportingBlock(got) {
		t.Errorf("Fable 5.1 rewritten to 5.2 must drop the injected Reporting outcomes block: %s", got)
	}

	rewritten = []byte(strings.Replace(string(cloaked), `"model":"claude-fable-5-1"`, `"model":"claude-mythos-5-2"`, 1))
	got = reconcileClaudeCodeFableModelAfterPayload(rewritten, state, false, false, true, false)
	if ojson.GetBytes(got, "fallbacks").Exists() {
		t.Errorf("Fable 5.1 rewritten to Mythos 5.2 must drop the injected fallback: %s", got)
	}

	got = reconcileClaudeCodeFableModelAfterPayload(
		[]byte(`{"model":"claude-fable-5-2","system":[]}`), claudeCodeFableState{}, false, false, true, false,
	)
	if fallback := ojson.GetBytes(got, "fallbacks.0.model").String(); fallback != "claude-opus-5" {
		t.Errorf("model rewritten to Fable 5.2 fallback = %q, want claude-opus-5", fallback)
	}
}

func quoteJSON(s string) string {
	doc := ojson.NewDoc([]byte(`{}`))
	_ = doc.Set("v", s)
	return ojson.GetBytes(doc.Bytes(), "v").Raw
}

// Models missing from the registry keep the caller's thinking mode. The docs
// reject block_binding with enabled thinking on every prefix-check model, so
// the injection must leave such requests untouched instead of causing a 400.
func TestClaudeExecutor_PreservedThinkingSkipsEnabledThinking(t *testing.T) {
	for _, model := range []string{"claude-mythos-5-1", "claude-mythos-5-2"} {
		t.Run(model, func(t *testing.T) {
			payload := strings.Replace(string(preservedThinkingReplayPayload(model)),
				`"thinking": {"type": "adaptive"}`, `"thinking": {"type": "enabled","budget_tokens":8000}`, 1)
			if payload == string(preservedThinkingReplayPayload(model)) {
				t.Fatal("test payload did not switch to enabled thinking")
			}
			seenBody, _ := runClaudeExecutorCapture(t, nil, "", false, model, []byte(payload))
			if got := ojson.GetBytes(seenBody, "thinking.type").String(); got != "enabled" {
				t.Fatalf("thinking.type = %q, want enabled to reach this path; body=%s", got, seenBody)
			}
			if ojson.GetBytes(seenBody, "thinking.block_binding").Exists() {
				t.Fatalf("block_binding with enabled thinking is rejected upstream: %s", seenBody)
			}
		})
	}
}
