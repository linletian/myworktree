package gitx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

func GitRoot(dir string) (string, error) {
	cmd := GitCommand(2*time.Second, dir, "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("not a git repository (or git not available)")
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("failed to determine git root")
	}
	return filepath.Clean(root), nil
}

func HashPath(p string) string {
	s := sha256.Sum256([]byte(p))
	return hex.EncodeToString(s[:])[:16]
}
