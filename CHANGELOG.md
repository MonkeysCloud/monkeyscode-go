# Changelog

`github.com/MonkeysCloud/monkeyscode-go`. Semantic versioning from v1.0.0:
breaking API changes only in a new major version (a `/v2` module path).

## Unreleased (planned v1.1.0, with the Auto mode release)

Version is not bumped yet: bumping `Version` in `version.go` publishes.
The release runbook does it on release day.

### Added
- Auto mode: `CLIResult.PlanRequired` (the server's plan card when Auto needs
  a paid plan, exit 4), `PlanRequiredError` (unwraps to `*ProcessError`) and
  `RequireSuccess(result)`.
- `CLIEvent.Route` for `system/route` events (which model ran a step).
- README: "Using Auto mode".

## v1.0.0

The first release published from `packages/sdk-go` in the MonkeysCode
monorepo. v0.1.0 came from an older copy that stopped receiving changes.

### Breaking
- The v0.1.0 API (`Config`, `DefaultConfig`, `Client.Agent`, `Agent.Run`,
  `Agent.Stream`, `Agent.Goal`, `RunResult`, `GoalOptions`, `GoalResult`) is
  gone. `NewClient` now takes `Options` and returns `*Client` without an
  error. See "Upgrading from v0.1.0" in the README.
- Requires Go 1.22 (was 1.21).

### Added
- `Query` / `Open`: run the agent locally through `mc` (1.0.0 or newer) over
  stream-json `schema_version` 1, with multi-turn sessions, `CanUseTool`
  permission callbacks (`Allow`, `Deny`, `AllowWith`), interrupts, and
  cost and token guards.
- `ErrCLITooOld`: `Wait` reports it when the installed `mc` predates
  stream-json, with the upgrade command, instead of a bare exit code.
- `ErrHostedRunUnavailable`: `Client.Run` / `Stream` wrap it on a 404
  from the hosted run endpoint, which is not deployed yet.
- `Version` and `MinCLIVersion` constants. The User-Agent, MCP client info,
  OTLP scope and SARIF driver all report `Version` instead of separate
  hard-coded strings.
- Runs, sessions, hooks, MCP, sandbox, subagents, orchestrator, file
  watcher, telemetry, OTLP export, transcript export, and SARIF/CI helpers.
- LICENSE (MIT), which the v0.1.0 README referred to but never shipped.

## v0.1.0

Initial release: `Client.Agent(dir)` with `Run`, `Stream` and `Goal` against
the hosted endpoint.
