# MonkeysCode Go SDK

```bash
go get github.com/MonkeysCloud/monkeyscode-go@latest
```

Run the MonkeysCode agent from Go. Standard library only; Go 1.22+.

`Query` and `Open` drive the `mc` CLI installed **on this machine**, so your
settings, permission rules, hooks and MCP servers apply, and every tool call
the agent wants to make can be approved or refused from your code. They need
`mc` 1.0.0 or newer:

```bash
npm i -g monkeyscode-cli@latest
mc auth login            # or set MONKEYSCODE_API_KEY
```

## One prompt

```go
package main

import (
	"context"
	"fmt"
	"log"

	monkeyscode "github.com/MonkeysCloud/monkeyscode-go"
)

func main() {
	ctx := context.Background()
	events, sess, err := monkeyscode.Query(ctx, "fix the failing test", monkeyscode.CLIOptions{
		AllowedTools: []string{"read_file", "run_command(npm test:*)"},
		MaxCostUSD:   1,
		CanUseTool: func(ctx context.Context, tool string, input map[string]any, pc monkeyscode.PermissionContext) monkeyscode.PermissionResult {
			if tool == "edit_file" {
				return monkeyscode.Allow()
			}
			return monkeyscode.Deny("read-only please")
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	for ev := range events {
		if ev.Type == "result" {
			fmt.Println(ev.Result.Result, ev.Result.CostUSD)
		}
	}
	if err := sess.Wait(); err != nil {
		log.Fatal(err) // *ProcessError or *SchemaError
	}
	fmt.Println("exit code:", sess.ExitCode())
}
```

## Several turns

`Open(ctx, opts)` starts an idle session. Call `sess.Send(text)` for each
turn and read `events` up to that turn's `result`.

- `sess.Interrupt()` stops the current turn only.
- `sess.End()` closes the session once pending turns finish.
- `sess.Close()`, or cancelling `ctx`, kills it.

## Permissions

With `CanUseTool` set, every tool call that your settings don't already allow
or deny is sent to your function, which returns `Allow()`, `Deny(reason)`, or
`AllowWith(input)` to allow with rewritten input. A panicking callback denies. Without a
callback, those calls are denied and the result says how to allow them.

## Errors and exit codes

`sess.Wait()` returns:

- `*ProcessError`: `mc` could not start, or exited without a result.
  `errors.Is(err, monkeyscode.ErrCLITooOld)` means the installed `mc`
  predates this protocol; upgrade it as above.
- `*SchemaError`: `mc` speaks a newer event schema than this SDK. Upgrade
  the SDK.

A run that finishes but fails is not an error from `Wait`. Check
`sess.ExitCode()`:

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | runtime error (model, tool or network failure after retries) |
| `2` | usage error (bad flags or options) |
| `3` | authentication error (not logged in) |
| `4` | quota or plan limit reached |
| `5` | `MaxCostUSD` / `MaxTokens` guard tripped |
| `6` | permission denied (every mutating tool call was refused) |
| `124` | `TimeoutMs` reached |
| `130` | interrupted |

`MaxCostUSD` is checked after each model response, so a run can go over by
at most one response. For a model the CLI has no price for, the cost can only
be enforced if the endpoint reports it; set `MaxTokens` as well.

`monkeyscode.RequireSuccess(result)` turns an error result into an error:
a `*PlanRequiredError` for a plan-gated one (below), a `*ProcessError`
otherwise, and `nil` on success.

## Using Auto mode

Auto mode lets MonkeysCode pick the model for each step: Capuchin by default,
a frontier model only when the task needs it. Set `Model: "auto-mode"` (or
`"monkeyscode-auto"`), or `"auto-max"` for Max quality. The plain `"auto"`
still means your default model, not Auto mode. Auto needs a Pro plan or
above (Max: Pro+); on other plans the result has exit code 4 and
`Result.PlanRequired` set.

```go
err := monkeyscode.RequireSuccess(result)
var pr *monkeyscode.PlanRequiredError
if errors.As(err, &pr) {
	fmt.Println(pr.Error(), pr.UpgradeURL())
	if pr.Plan.FallbackModel != "" { // e.g. capuchin-reason
		// run again with Model: pr.Plan.FallbackModel
	}
}
```

`*PlanRequiredError` unwraps to a `*ProcessError`, so existing `errors.As`
checks still match. Each `system/route` event has `ev.Route` set (requested,
routed, reason, escalations used); it is informational, and the routed model
can change from step to step.

## Finding `mc`

`PathToMc`, then `$MC_PATH`, then `mc` on `PATH`. The events are the ones
`mc -p -o stream-json` prints, at `schema_version` 1
(`monkeyscode.SupportedSchemaVersion`).

## `Client` (hosted API)

`NewClient(Options{...})` and `Client.Run` / `Client.Stream` call a hosted
run endpoint that is not deployed yet. Against the default endpoint they
return an error wrapping `ErrHostedRunUnavailable`. Use `Query` / `Open`.

The rest of the package (runs, sessions, hooks, MCP, sandbox, telemetry,
SARIF/CI helpers) is documented on
[pkg.go.dev](https://pkg.go.dev/github.com/MonkeysCloud/monkeyscode-go).

## Upgrading from v0.1.0

v1.0.0 replaces the v0.1.0 API, which only ever talked to the hosted
endpoint above:

| v0.1.0 | v1.0.0 |
|---|---|
| `NewClient(&Config{...})` returning `(*Client, error)` | `Query` / `Open` with `CLIOptions`. `NewClient(Options{...})` still exists for the hosted API and returns `*Client` |
| `DefaultConfig()` | zero-value `CLIOptions{}` |
| `client.Agent(dir).Run(ctx, p)` | `Query(ctx, p, CLIOptions{Cwd: dir})` |
| `agent.Stream(ctx, p)` | the `events` channel from `Query` / `Open` |
| `agent.Goal(...)`, `GoalOptions`, `GoalResult` | removed |
| `RunResult` | `CLIResult` (`ev.Result`, `sess.LastResult()`) |

Pin `@v0.1.0` if you need the old API.

## Links

- [MonkeysCode CLI](https://monkeyscode.com/docs/code-agent/cli)
- [Changelog](CHANGELOG.md)

## License

MIT. See [LICENSE](LICENSE).
