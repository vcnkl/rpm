package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"github.com/vcnkl/rpm/pathsafe"
)

func OpenFile(repoRoot string, out string, nested ...string) (io.WriteCloser, error) {
	if filepath.IsAbs(out) {
		return nil, errors.Errorf("log output path %q must be relative to the repo root", out)
	}
	base, err := pathsafe.Resolve(repoRoot, out)
	if err != nil {
		return nil, errors.Wrap(err, "resolve log output path")
	}
	for _, part := range nested {
		if filepath.IsAbs(part) {
			return nil, errors.Errorf("nested log output path %q must be relative", part)
		}
	}
	dir, err := pathsafe.Resolve(base, filepath.Join(nested...))
	if err != nil {
		return nil, errors.Wrap(err, "resolve nested log output path")
	}
	if err = validatePath(repoRoot, dir); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, errors.Wrapf(err, "create log output directory %s", dir)
	}
	if err = validatePath(repoRoot, dir); err != nil {
		return nil, err
	}
	for millis := time.Now().UTC().UnixMilli(); ; millis++ {
		path := filepath.Join(dir, fmt.Sprintf("%d.txt", millis))
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if openErr == nil {
			return file, nil
		}
		if !os.IsExist(openErr) {
			return nil, errors.Wrapf(openErr, "create log file %s", path)
		}
	}
}

func validatePath(repoRoot string, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return errors.Wrap(err, "resolve repo root")
	}
	existing := path
	for {
		_, statErr := os.Lstat(existing)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			return errors.Wrapf(statErr, "inspect log output path %s", existing)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return errors.Errorf("log output path %q has no existing ancestor", path)
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return errors.Wrap(err, "resolve log output path")
	}
	if !pathsafe.Contains(resolvedRoot, resolved) {
		return errors.Errorf("log output path %q resolves outside repo root %q", path, repoRoot)
	}
	return nil
}
