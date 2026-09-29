package monkeyscode

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WatchOptions configures the file watcher.
type WatchOptions struct {
	Directory      string
	Ignore         []string
	DebounceMs     int
	PollIntervalMs int
}

var defaultIgnores = []string{
	"node_modules", ".git", "dist", "build", ".next", ".nuxt",
	"target", "__pycache__", ".monkeyscode", ".DS_Store",
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "Cargo.lock",
	".venv", "venv", ".mypy_cache", ".ruff_cache", ".pytest_cache",
}

// WatchFiles watches a directory and sends batches of changed files to the channel.
// Close the done channel to stop watching.
func WatchFiles(opts WatchOptions, done <-chan struct{}) <-chan []string {
	ch := make(chan []string, 16)

	if opts.Directory == "" {
		opts.Directory = "."
	}
	if opts.DebounceMs <= 0 {
		opts.DebounceMs = 500
	}
	if opts.PollIntervalMs <= 0 {
		opts.PollIntervalMs = 1000
	}

	ignores := make(map[string]bool)
	for _, ign := range defaultIgnores {
		ignores[ign] = true
	}
	for _, ign := range opts.Ignore {
		ignores[ign] = true
	}

	go func() {
		defer close(ch)

		snapshot := buildSnapshot(opts.Directory, ignores)

		for {
			select {
			case <-done:
				return
			case <-time.After(time.Duration(opts.PollIntervalMs) * time.Millisecond):
			}

			newSnapshot := buildSnapshot(opts.Directory, ignores)
			changed := diffSnapshots(snapshot, newSnapshot)

			if len(changed) > 0 {
				// Debounce
				select {
				case <-done:
					return
				case <-time.After(time.Duration(opts.DebounceMs) * time.Millisecond):
				}

				finalSnapshot := buildSnapshot(opts.Directory, ignores)
				allChanged := diffSnapshots(snapshot, finalSnapshot)
				snapshot = finalSnapshot

				if len(allChanged) > 0 {
					// Sort
					sorted := make([]string, len(allChanged))
					copy(sorted, allChanged)
					// Simple sort
					for i := 0; i < len(sorted); i++ {
						for j := i + 1; j < len(sorted); j++ {
							if sorted[j] < sorted[i] {
								sorted[i], sorted[j] = sorted[j], sorted[i]
							}
						}
					}
					ch <- sorted
				}
			} else {
				snapshot = newSnapshot
			}
		}
	}()

	return ch
}

// FormatChangeBatch formats a batch of changed files for display.
func FormatChangeBatch(batch []string) string {
	if len(batch) == 0 {
		return "No changes"
	}
	if len(batch) == 1 {
		return "Changed: " + batch[0]
	}
	var b strings.Builder
	b.WriteString("Changed ")
	b.WriteString(strings.Repeat("", 0)) // noop
	b.WriteString(string(rune('0'+len(batch)/10)) + string(rune('0'+len(batch)%10)))
	b.WriteString(" files")
	// Actually just use fmt
	return strings.Join(append([]string{""}, batch...), "\n  ")
}

func buildSnapshot(dir string, ignores map[string]bool) map[string]int64 {
	snapshot := make(map[string]int64)

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		name := info.Name()
		if info.IsDir() {
			if shouldIgnore(name, ignores) {
				return filepath.SkipDir
			}
			return nil
		}

		if shouldIgnore(name, ignores) {
			return nil
		}

		snapshot[path] = info.ModTime().UnixNano()
		return nil
	})

	return snapshot
}

func diffSnapshots(old, new map[string]int64) []string {
	var changed []string

	for path, mtime := range new {
		oldMtime, exists := old[path]
		if !exists || mtime > oldMtime {
			changed = append(changed, path)
		}
	}

	for path := range old {
		if _, exists := new[path]; !exists {
			changed = append(changed, path)
		}
	}

	return changed
}

func shouldIgnore(name string, ignores map[string]bool) bool {
	if strings.HasPrefix(name, ".") {
		if name != ".env" && name != ".env.local" && name != ".env.development" {
			return true
		}
	}
	return ignores[name]
}
