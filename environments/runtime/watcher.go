package runtime

import (
	"context"
	"path/filepath"

	envstarlark "github.com/vcnkl/rpm/environments/starlark"
	"github.com/vcnkl/rpm/watcher"
)

type WatcherFactory struct{}

func NewWatcherFactory() *WatcherFactory {
	return &WatcherFactory{}
}

func (WatcherFactory) Watch(ctx context.Context, watches []envstarlark.Watch, onChange func(target string, path string)) error {
	type targetWatch struct {
		target string
		roots  []string
		ignore []string
	}
	entries := make([]targetWatch, 0, len(watches))
	for _, watch := range watches {
		roots := make([]string, 0, len(watch.Roots))
		for _, root := range watch.Roots {
			if matches, err := filepath.Glob(root); err == nil && len(matches) > 0 {
				roots = append(roots, matches...)
				continue
			}
			roots = append(roots, root)
		}
		entries = append(entries, targetWatch{target: watch.Target, roots: roots, ignore: watch.Ignore})
	}

	errCh := make(chan error, len(entries))
	for _, entry := range entries {
		w, err := watcher.NewWatcher(entry.roots, entry.ignore)
		if err != nil {
			return err
		}
		entry := entry
		w.OnChange(func(path string) {
			onChange(entry.target, path)
		})
		go func() {
			errCh <- w.Start(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
