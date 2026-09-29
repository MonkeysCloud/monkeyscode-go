package monkeyscode

import (
	"errors"
	"strings"
)

// Version is this SDK's release. .github/workflows/sync-go-sdk.yml tags the
// public repo v<Version> when it syncs a version that has no tag yet, so
// bumping this constant is how a release is cut. Semver: patch for fixes,
// minor for additions; a breaking change needs a new major and a /vN module
// path, so avoid one.
const Version = "1.0.0"

// MinCLIVersion is the oldest `mc` that speaks the stream-json protocol
// Query and Open use (`-p --input-format stream-json -o stream-json`,
// `--permission-prompt stdio`). Older releases reject those flags.
const MinCLIVersion = "1.0.0"

// userAgent identifies this SDK in HTTP requests.
const userAgent = "monkeyscode-sdk-go/" + Version

// ErrHostedRunUnavailable is wrapped by Client.Run/Stream when the endpoint
// has no hosted agent route. Use Query/Open instead.
var ErrHostedRunUnavailable = errors.New("monkeyscode: hosted agent run is not available")

// ErrCLITooOld is wrapped in the ProcessError returned when the installed
// `mc` exits before speaking stream-json because it predates it.
var ErrCLITooOld = errors.New("monkeyscode: installed mc is too old for Query/Open")

// oldCLIMarkers are commander's usage errors from an `mc` that predates
// stream-json: it doesn't know one of the flags BuildCLIArgs always sends,
// or not the format value. Checked against the real 0.1.12, which fails on
// `unknown option '--output-format'`.
var oldCLIMarkers = []string{
	"unknown option '--output-format'",
	"unknown option '--input-format'",
	"unknown option '--permission-prompt'",
	"unknown option '--print'",
	"'stream-json' is invalid",
}

func looksLikeOldCLI(stderr string) bool {
	for _, m := range oldCLIMarkers {
		if strings.Contains(stderr, m) {
			return true
		}
	}
	return false
}
