// Package memory provides the memory / knowledge-base subsystem.
package memory

import "time"

// MemoryHeader holds metadata about a single memory file.
type MemoryHeader struct {
	Path        string    `json:"path"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	ModifiedAt  time.Time `json:"modified_at"`
}
