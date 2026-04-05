package memory

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openharness/openharness/pkg/config"
)

// GetProjectMemoryDir returns the sha1-based per-project memory directory
// inside the global data dir, mirroring the Python implementation.
func GetProjectMemoryDir(cwd string) string {
	h := sha1.Sum([]byte(cwd))
	hash := fmt.Sprintf("%x", h[:10]) // 20 hex chars
	return filepath.Join(config.GetDataDir(), "memory", hash)
}

// GetGlobalMemoryDir returns the global (non-project-specific) memory dir.
func GetGlobalMemoryDir() string {
	return filepath.Join(config.GetDataDir(), "memory", "_global")
}

// EnsureMemoryDir creates the memory directory if it does not exist.
func EnsureMemoryDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
