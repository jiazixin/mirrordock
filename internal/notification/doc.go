// Package notification stores encrypted channel credentials and delivers run
// result notifications through a persistent send queue with bounded retries.
// New constructs without starting work. Start follows listener binding; Shutdown
// must finish before closing shared storage.
package notification
