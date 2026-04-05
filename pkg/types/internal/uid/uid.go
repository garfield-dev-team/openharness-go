// Package uid provides a tiny helper to generate hex UUIDs without
// pulling in external dependencies.
package uid

import (
	"crypto/rand"
	"encoding/hex"
)

// NewHex returns a 32-character lowercase hex string (128-bit random),
// equivalent to Python uuid4().hex.
func NewHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
