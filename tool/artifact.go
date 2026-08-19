package tool

import (
	openagent "github.com/yusheng-g/openagent-go"
)

// ArtifactRoot returns the platform-appropriate artifact directory.
// Linux/macOS: /tmp/<version.Name> (default /tmp/openagent)
// Windows:     %TEMP%\<version.Name>
//
// Delegates to openagent.ArtifactRoot so there is a single source of truth
// for the tmp root (the openagent package owns the version.Name sanitization).
// Tool results exceeding a size threshold can be saved here by hooks and
// referenced in the tool result summary. The system tmp cleaner reclaims
// the space eventually, so artifacts are best-effort persistent.
func ArtifactRoot() string {
	return openagent.ArtifactRoot()
}
