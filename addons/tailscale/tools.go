//go:build tools

// Package tailscale pins Tailscale and every module it is built from
// (go.mod, go.sum). The add-on's program is tailscale.com/cmd/tailscaled.
package tailscale

import _ "tailscale.com/cmd/tailscaled"
