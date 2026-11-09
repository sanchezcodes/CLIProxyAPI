package executor

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

// Claude Code 2.1.321 counts every user turn (turnIndex) and the human prompts
// among them (promptIndex); tool-result turns advance only turnIndex.
func TestClaudeTurnPositionDoc(t *testing.T) {
	for _, tc := range []struct {
		name               string
		messages           string
		wantPrompt, wantTn int
		wantOK             bool
	}{
		{name: "fresh conversation", messages: `[{"role":"user","content":"hi"}]`, wantPrompt: 1, wantTn: 1, wantOK: true},
		{
			name: "tool loop advances only the turn",
			messages: `[{"role":"user","content":"hi"},
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]`,
			wantPrompt: 1, wantTn: 2, wantOK: true,
		},
		{
			name: "second human prompt advances both",
			messages: `[{"role":"user","content":"hi"},
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},
				{"role":"assistant","content":"done"},
				{"role":"user","content":[{"type":"text","text":"next"}]}]`,
			wantPrompt: 2, wantTn: 3, wantOK: true,
		},
		{name: "no user turn", messages: `[]`, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := ojson.NewDoc([]byte(`{"messages":` + tc.messages + `}`))
			prompt, turn, ok := claudeTurnPositionDoc(doc)
			if ok != tc.wantOK || (ok && (prompt != tc.wantPrompt || turn != tc.wantTn)) {
				t.Fatalf("claudeTurnPositionDoc() = %d/%d ok=%v, want %d/%d ok=%v", prompt, turn, ok, tc.wantPrompt, tc.wantTn, tc.wantOK)
			}
		})
	}
}

// A signed main-thread request carries the 2.1.321 turn position right after
// cc_turn_origin; probes and unsigned paths do not.
func TestClaudeBillingHeaderCarriesTurnPosition(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`)

	doc := ojson.NewDoc(body)
	checkSystemInstructionsWithSigningModeAtDoc(doc, false, true, "2.1.321", "cli", "", "2026-11-09", false, "", "", false, "human")
	billing := doc.Get("system.0.text").String()
	if !strings.HasSuffix(billing, " cc_turn_origin=human; cc_prompt_index=1; cc_turn_index=2;") {
		t.Fatalf("billing header = %q, want turn position after cc_turn_origin", billing)
	}

	unsigned := ojson.NewDoc(body)
	checkSystemInstructionsWithSigningModeAtDoc(unsigned, false, false, "2.1.321", "cli", "", "2026-11-09", false, "", "", false, "human")
	if got := unsigned.Get("system.0.text").String(); strings.Contains(got, "cc_prompt_index") {
		t.Fatalf("unsigned billing header = %q, want no turn position", got)
	}

	noOrigin := ojson.NewDoc(body)
	checkSystemInstructionsWithSigningModeAtDoc(noOrigin, false, true, "2.1.321", "cli", "", "2026-11-09", false, "", "", false, "")
	if got := noOrigin.Get("system.0.text").String(); strings.Contains(got, "cc_prompt_index") {
		t.Fatalf("billing header without turn origin = %q, want no turn position", got)
	}
}

// Pins the 2.1.321 wire order measured on 2026-11-09 against a native capture
// of the same account: no thinking-token-count or prompt-caching-scope in the
// constant prefix, no mid-conversation-system, inline-tools after the tool
// changes beta, and thinking-token-count after advisor-tool.
func TestClaudeCodeCLIBetas_Measured321MainRequest(t *testing.T) {
	body := `{"model":"claude-fable-5-2","thinking":{"type":"adaptive","display":"updates"},` +
		`"tools":[{"type":"advisor_20260301","name":"advisor","model":"claude-opus-5-5"}],` +
		`"messages":[{"role":"user","content":"hi"}]}`
	want := "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14," +
		"context-management-2025-06-27,per-turn-control-2026-07-01,mid-conversation-tool-changes-2026-07-01," +
		"inline-tools-2026-09-15,advisor-tool-2026-03-01,thinking-token-count-2026-05-13," +
		"mid-conversation-system-clear-at-2026-08-21,effort-2025-11-24,thinking-binding-controls-2026-08-01," +
		"thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11"
	if got := claudeCodeCLIBetas([]byte(body), nil, true); got != want {
		t.Fatalf("claudeCodeCLIBetas() =\n %s\nwant\n %s", got, want)
	}
	for _, retired := range []string{claudeMidConvSystemBeta, claudePromptCachingScopeBeta, claudeRedactThinkingBeta} {
		if got := claudeCodeCLIBetas([]byte(body), nil, true); strings.Contains(got, retired) {
			t.Fatalf("claudeCodeCLIBetas() = %q, want no %s", got, retired)
		}
	}
}

// 2.1.321 sends dangerous-tool-use after effort and the fallback betas.
func TestClaudeCodeCLIBetas_DangerousToolUseFollowsEffort(t *testing.T) {
	body := `{"model":"claude-opus-5-5","thinking":{"type":"adaptive"},"safeguards":[{"type":"dangerous_tool_use"}],"fallbacks":[{"model":"claude-opus-4-8"}]}`
	got := claudeCodeCLIBetas([]byte(body), nil, true)
	effort := strings.Index(got, claudeEffortBeta)
	credit := strings.Index(got, claudeFallbackCreditBeta)
	dangerous := strings.Index(got, claudeDangerousToolUseBeta)
	if effort < 0 || credit < 0 || dangerous < 0 || !(effort < credit && credit < dangerous) {
		t.Fatalf("claudeCodeCLIBetas() = %q, want effort, fallback-credit, then dangerous-tool-use", got)
	}
}
