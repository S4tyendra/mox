package store

import "github.com/mjl-/mox/organizer"

// OrganizerSettings are private per-account settings, separate from the public
// webmail Settings and SSE payload (which must never contain provider API keys).
type OrganizerSettings struct {
	ID         uint8
	Enabled    bool
	Tags       []string
	Rules      []organizer.Rule
	AIEndpoint string
	AIModel    string
	AIKey      string
	MCPEnabled bool
}
