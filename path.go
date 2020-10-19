package servicetracer

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

var (
	cacheDir = ".service-tracer-cache"
)

func mapsDir() string {
	return filepath.Join(cacheDir, "maps")
}

func ServiceMapFile(service *Service) string {
	return filepath.Join(cacheDir, "maps", fmt.Sprintf("%s.yaml", service.Name))
}

func RepoRoot(service *Service) string {
	return filepath.Join(cacheDir, service.RepoName())
}

func ProtoRepoRoot(service *Service) string {
	return filepath.Join(cacheDir, service.ProtoRepoName())
}

func CreateCacheDir() error {
	dir := mapsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return xerrors.Errorf("failed to create directory %s: %w", dir, err)
	}
	return nil
}
