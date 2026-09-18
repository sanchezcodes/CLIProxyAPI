package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestIsRequestInvalidError_OutOfExtraUsageIsCredentialRetryable(t *testing.T) {
	workspaceAdmin := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    `{"type":"error","error":{"type":"invalid_request_error","message":"You're out of extra usage. Ask your workspace admin to add more so you can keep going."}}`,
	}
	settingsUsage := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    `{"type":"error","error":{"type":"invalid_request_error","message":"You're out of extra usage. Add more at claude.ai/settings/usage and keep going."}}`,
	}
	genuine := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    `{"type":"error","error":{"type":"invalid_request_error","message":"messages.0.content: Field required"}}`,
	}

	if isRequestInvalidError(workspaceAdmin) {
		t.Fatal("workspace-admin Extra Usage 400 must remain credential-retryable")
	}
	if isRequestInvalidError(settingsUsage) {
		t.Fatal("settings/usage Extra Usage 400 must remain credential-retryable")
	}
	if !isRequestInvalidError(genuine) {
		t.Fatal("genuine invalid_request_error must stay request-scoped")
	}
}

func TestManager_OutOfExtraUsageBadRequest_FallsBackAndCoolsAuth(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	extraUsageErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    `{"type":"error","error":{"type":"invalid_request_error","message":"You're out of extra usage. Ask your workspace admin to add more so you can keep going."}}`,
	}
	executor := &authFallbackExecutor{
		id: "claude",
		executeErrors: map[string]error{
			"aa-depleted-auth": extraUsageErr,
		},
	}
	manager.RegisterExecutor(executor)

	model := "claude-fable-5"
	depleted := &Auth{ID: "aa-depleted-auth", Provider: "claude"}
	healthy := &Auth{ID: "bb-healthy-auth", Provider: "claude"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(depleted.ID, "claude", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(healthy.ID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(depleted.ID)
		reg.UnregisterClient(healthy.ID)
	})

	if _, err := manager.Register(context.Background(), depleted); err != nil {
		t.Fatalf("register depleted auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), healthy); err != nil {
		t.Fatalf("register healthy auth: %v", err)
	}

	request := cliproxyexecutor.Request{Model: model}
	for i := 0; i < 2; i++ {
		resp, err := manager.Execute(context.Background(), []string{"claude"}, request, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d error = %v, want success via rotation", i, err)
		}
		if string(resp.Payload) != healthy.ID {
			t.Fatalf("execute %d payload = %q, want %q", i, string(resp.Payload), healthy.ID)
		}
	}

	got := executor.ExecuteCalls()
	want := []string{depleted.ID, healthy.ID, healthy.ID}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d auth = %q, want %q", i, got[i], want[i])
		}
	}

	updated, ok := manager.GetByID(depleted.ID)
	if !ok || updated == nil {
		t.Fatal("expected depleted auth to remain registered")
	}
	state := updated.ModelStates[model]
	if state == nil || !state.Unavailable || !state.Quota.Exceeded || state.NextRetryAfter.IsZero() {
		t.Fatalf("expected depleted model to enter quota cooldown, got %#v", state)
	}
}
