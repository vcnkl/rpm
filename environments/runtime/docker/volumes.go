package docker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
)

type VolumeNamer interface {
	Name(ctx context.Context, blueprint string, dependency string, path string) (string, error)
}

type randomDigitsFunc func() (string, error)

type MemoryVolumeNamer struct {
	projectName string
	random      randomDigitsFunc
	mu          sync.Mutex
	entries     volumeCache
}

func NewMemoryVolumeNamer(projectName string) *MemoryVolumeNamer {
	return &MemoryVolumeNamer{
		projectName: projectName,
		random:      randomSixDigits,
		entries:     make(volumeCache),
	}
}

func (n *MemoryVolumeNamer) Name(_ context.Context, blueprint string, dependency string, path string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return volumeName(n.entries, n.projectName, blueprint, dependency, path, n.random)
}

type FileVolumeNamer struct {
	path        string
	projectName string
	random      randomDigitsFunc
	mu          sync.Mutex
}

func NewFileVolumeNamer(path string, projectName string) *FileVolumeNamer {
	return &FileVolumeNamer{path: path, projectName: projectName, random: randomSixDigits}
}

func (n *FileVolumeNamer) Name(_ context.Context, blueprint string, dependency string, path string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	cache, err := readVolumeCache(n.path)
	if err != nil {
		return "", err
	}
	name, err := volumeName(cache, n.projectName, blueprint, dependency, path, n.random)
	if err != nil {
		return "", err
	}
	if err := writeVolumeCache(n.path, cache); err != nil {
		return "", err
	}
	return name, nil
}

func PruneVolumeCache(path string, blueprint string) error {
	cache, err := readVolumeCache(path)
	if err != nil {
		return err
	}
	if _, ok := cache[blueprint]; !ok {
		return nil
	}
	delete(cache, blueprint)
	return writeVolumeCache(path, cache)
}

type volumeCache map[string]map[string]map[string]string

func volumeName(cache volumeCache, projectName string, blueprint string, dependency string, path string, random randomDigitsFunc) (string, error) {
	if cache[blueprint] == nil {
		cache[blueprint] = make(map[string]map[string]string)
	}
	if cache[blueprint][dependency] == nil {
		cache[blueprint][dependency] = make(map[string]string)
	}
	if name := cache[blueprint][dependency][path]; name != "" {
		return name, nil
	}
	suffix, err := random()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s-%s", sanitize(projectName), sanitize(dependency), sanitize(blueprint), suffix)
	cache[blueprint][dependency][path] = name
	return name, nil
}

func readVolumeCache(path string) (volumeCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(volumeCache), nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return make(volumeCache), nil
	}
	var cache volumeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache == nil {
		cache = make(volumeCache)
	}
	return cache, nil
}

func writeVolumeCache(path string, cache volumeCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-volumes-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func randomSixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
