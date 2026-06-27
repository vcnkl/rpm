package pathsafe

import (
	"fmt"
	"path/filepath"
	"strings"
)

func Contains(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func Resolve(root, rel string) (string, error) {
	joined := filepath.Join(root, rel)
	if !Contains(root, joined) {
		return "", fmt.Errorf("path %q escapes %q", rel, root)
	}
	return joined, nil
}

func MatchSuffix(suffix, rel string) bool {
	suffix = strings.Trim(filepath.ToSlash(suffix), "/")
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if suffix == "" {
		return true
	}
	sufSegs := strings.Split(suffix, "/")
	relSegs := strings.Split(rel, "/")
	if len(relSegs) < len(sufSegs) {
		return false
	}
	offset := len(relSegs) - len(sufSegs)
	for i, seg := range sufSegs {
		matched, err := filepath.Match(seg, relSegs[offset+i])
		if err != nil || !matched {
			return false
		}
	}
	return true
}
