# Fork upgrade and deploy (VPS)

This fork (`sanchezcodes/CLIProxyAPI`, branch `stable`) runs on the VPS as the
personal multi-account gateway. Upgrading to a new upstream release is routine;
follow these steps in order.

## Deployment layout

- Service: systemd user unit `cliproxyapi` (`~/.config/systemd/user/cliproxyapi.service`).
- Binary: `~/.local/bin/cli-proxy-api` is a symlink to `cli-proxy-api` at the repo root,
  so building at the repo root replaces the running binary on the next restart.
- Config: `~/.cli-proxy-api/config.yaml`. Credentials: `~/.cli-proxy-api/*.json`. Logs:
  `~/.cli-proxy-api/logs/main.log` (journald only shows start/stop).
- Listener: the Tailscale IP only (`100.67.58.20:8317`). The service stays tailnet-only;
  it is deliberately not published to the internet (Funnel, tunnels) for hosted clients
  such as Amp — account-ban risk and exposure outweigh the benefit.
- Go toolchain: `~/.local/go/bin` (not on the default `PATH`).

## Upgrade steps

1. **Merge.** `git fetch upstream --tags`, then on `stable`:
   `git merge --no-ff vX.Y.Z` with message `merge(upstream): update CLIProxyAPI to vX.Y.Z`.
   Resolve conflicts keeping the fork patches (below) unless upstream now covers them.
   Done when `git diff vX.Y.Z HEAD` shows only intended fork deltas.
2. **Test.** `go test ./...` passes. When a failure appears, run the same package at the
   bare tag in a `git worktree` to tell upstream breakage from fork-patch breakage.
   `gofmt -l` noise in files untouched by the fork is upstream's; leave it.
3. **Back up the running binary** outside the repo:
   `mkdir -p ~/.local/share/cliproxyapi && cp -p cli-proxy-api ~/.local/share/cliproxyapi/cli-proxy-api.prev`.
4. **Build with version ldflags.** A plain `go build` reports version `dev`, and the
   management panel's "Check for updates" then fails with
   "Server version is unavailable; cannot compare".
   ```bash
   V=$(git describe --tags --abbrev=0) C=$(git rev-parse --short HEAD) D=$(date -u +%Y-%m-%dT%H:%M:%SZ)
   go build -ldflags="-X 'main.Version=$V' -X 'main.Commit=$C' -X 'main.BuildDate=$D'" -o cli-proxy-api.new ./cmd/server
   mv cli-proxy-api.new cli-proxy-api
   ```
5. **Restart:** `systemctl --user restart cliproxyapi`.
6. **Verify with real traffic.** `is-active` only proves the process started; a release can
   start cleanly and still get every Claude request rejected upstream. Done when all hold:
   - a third-party-client probe to `/v1/messages` returns 200 (script below);
   - `main.log` shows no new `429` / `cooldown` lines after the restart;
   - the management endpoint returns header `X-Cpa-Version: vX.Y.Z`.
   ```bash
   KEY=$(awk '/^api-keys:/{getline; print; exit}' ~/.cli-proxy-api/config.yaml | sed -E 's/^[^"]*"([^"]+)".*/\1/')
   curl -s -o /tmp/probe.json -w '%{http_code}\n' http://100.67.58.20:8317/v1/messages \
     -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -H 'anthropic-version: 2023-06-01' \
     -H 'User-Agent: pi (linux; x64)' \
     -d '{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"Reply: ok"}]}'
   unset KEY; rm -f /tmp/probe.json
   curl -s -D - -o /dev/null http://100.67.58.20:8317/v0/management/config | grep -i x-cpa-version
   ```
   The key extraction keeps the value out of the terminal; a `401 Invalid API key` from the
   probe means the extraction broke, not the release.
7. **Push:** `git push origin stable`.

## Rollback

Restore with `mv`, never `cp` over the running binary (`cp` fails with "Text file busy"):

```bash
cp -p ~/.local/share/cliproxyapi/cli-proxy-api.prev cli-proxy-api.rb && mv cli-proxy-api.rb cli-proxy-api
systemctl --user restart cliproxyapi
```

## Fork patches to carry forward

- **Claude extra-usage rotation** (`sdk/cliproxy/auth/conductor_cooldown.go`): Anthropic's
  "out of extra usage" 400 is promoted to 429 so the credential cools down and failover
  continues. Mirrors upstream PRs #3305/#3101 (unmerged as of v7.3.17).
- **Claude detection floor** (`claudeDetectionBaseline` in
  `internal/runtime/executor/helps/claude_client_detection.go`): native Claude Code
  user agents from 2.1.258 up keep passthrough. Exact helper-header matching follows
  upstream's measured baseline.

## Credential settings that upgrades depend on

Both `~/.cli-proxy-api/claude-*.json` carry `"cloak_mode": "always"`. Since v7.3.17
(upstream #6096, reported as #6120), direct `/v1/messages` requests from non-Claude-Code
clients (pi) on OAuth credentials are forwarded uncloaked unless cloaking is configured,
and Anthropic answers `429 rate_limit_error "Error"` on every account. Genuine Claude
Code stays passthrough under `always`. Add the field to every new Claude OAuth credential.
