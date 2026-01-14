package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	paths    []string
	ignore   []string
	onChange func(path string)
	fsw      *fsnotify.Watcher
	mu       sync.Mutex
}

func NewWatcher(paths []string, ignore []string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	return &Watcher{
		paths:  paths,
		ignore: ignore,
		fsw:    fsw,
	}, nil
}

func (w *Watcher) OnChange(fn func(path string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = fn
}

func (w *Watcher) Start(ctx context.Context) error {
	for _, path := range w.paths {
		if err := w.addRecursive(path); err != nil {
			return fmt.Errorf("failed to watch path %s: %w", path, err)
		}
	}

	debouncer := NewDebouncer(100 * time.Millisecond)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.fsw.Events:
				if !ok {
					return
				}

				if w.shouldIgnore(event.Name) {
					continue
				}

				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
					debouncer.Trigger(func() {
						w.mu.Lock()
						fn := w.onChange
						w.mu.Unlock()

						if fn != nil {
							fn(event.Name)
						}
					})
				}

				if event.Op&fsnotify.Create != 0 {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						w.addRecursive(event.Name)
					}
				}
			case err, ok := <-w.fsw.Errors:
				if !ok {
					return
				}
				if err != nil {
				}
			}
		}
	}()

	<-ctx.Done()
	return nil
}

func (w *Watcher) Stop() {
	w.fsw.Close()
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".venv" || base == "__pycache__" {
				return filepath.SkipDir
			}

			if w.shouldIgnore(path) {
				return filepath.SkipDir
			}

			if err = w.fsw.Add(path); err != nil {
				return nil
			}
		}

		return nil
	})
}

func (w *Watcher) shouldIgnore(path string) bool {
	for _, pattern := range w.ignore {
		if strings.Contains(pattern, "**") {
			pattern = strings.ReplaceAll(pattern, "**", "*")
		}

		pattern = strings.TrimPrefix(pattern, "./")
		pathRel := path

		matched, err := filepath.Match(pattern, pathRel)
		if err == nil && matched {
			return true
		}

		if strings.Contains(path, strings.TrimSuffix(strings.TrimPrefix(pattern, "*"), "*")) {
			return true
		}
	}

	return false
}
