# MonkeysCode Go SDK

Official Go SDK for [MonkeysCode](https://monkeyscode.com) — programmatic agent automation.

## Installation

```bash
go get github.com/MonkeysCloud/monkeyscode-go
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	monkeyscode "github.com/MonkeysCloud/monkeyscode-go"
)

func main() {
	client, err := monkeyscode.NewClient(&monkeyscode.Config{
		APIKey: "your-api-key", // or set MONKEYSCODE_API_KEY env var
	})
	if err != nil {
		log.Fatal(err)
	}

	agent := client.Agent("./my-project")

	// Synchronous — wait for completion
	result, err := agent.Run(context.Background(), "Add error handling to main.go")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✅ %s (cost: $%.4f)\n", result.Summary, result.Cost)
}
```

## Streaming Events

```go
for ev := range agent.Stream(ctx, "Refactor the auth module") {
    switch ev.Type {
    case monkeyscode.EventText:
        fmt.Print(ev.Content)
    case monkeyscode.EventToolCall:
        fmt.Printf("🔧 %s\n", ev.Tool)
    case monkeyscode.EventFileEdit:
        fmt.Printf("📝 %s (+%d/-%d)\n", ev.Path, ev.LinesAdded, ev.LinesRemoved)
    case monkeyscode.EventComplete:
        fmt.Printf("\n✅ Done: %s\n", ev.Summary)
    case monkeyscode.EventError:
        fmt.Printf("❌ Error: %s\n", ev.Message)
    }
}
```

## Goal Mode

Run iteratively until a verification command passes:

```go
result, err := agent.Goal(ctx, "Fix all failing tests", monkeyscode.GoalOptions{
    VerifyCommand: "go test ./...",
    MaxIterations: 5,
    MaxCost:       1.0, // USD
})
fmt.Printf("Goal met: %v (%d iterations)\n", result.GoalMet, result.Iterations)
```

## Configuration

```go
cfg := &monkeyscode.Config{
    APIKey:          "your-key",          // Required (or MONKEYSCODE_API_KEY)
    ProxyURL:        "https://api.monkeyscode.com", // Default
    Model:           "capuchin-reason",   // Default
    Timeout:         5 * time.Minute,     // Default
    MaxTurns:        50,                  // Default
    Sandboxed:       true,                // Default
    PermissionsMode: "auto-approve",      // Default
}
```

## License

MIT — see [LICENSE](LICENSE).
