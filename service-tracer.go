package servicetracer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"golang.org/x/xerrors"
)

type ServiceTracer struct {
	cfg      *Config
	analyzer *Analyzer
	renderer *Renderer
}

func New(cfg *Config) *ServiceTracer {
	return &ServiceTracer{
		cfg:      cfg,
		analyzer: NewAnalyzer(cfg),
		renderer: NewRenderer(cfg),
	}
}

func (t *ServiceTracer) Run() error {
	if err := CloneRepository(t.cfg); err != nil {
		return xerrors.Errorf("failed to clone repository: %w", err)
	}
	mapsDir := filepath.Join(cacheDir, "maps")
	if err := os.MkdirAll(mapsDir, 0755); err != nil {
		return xerrors.Errorf("failed to create directory %s: %w", mapsDir, err)
	}
	methodMap, err := t.createMethodMap()
	if err != nil {
		return xerrors.Errorf("failed to create method map: %w", err)
	}
	if err := t.renderer.Render(methodMap); err != nil {
		return xerrors.Errorf("failed to render method map: %w", err)
	}
	return nil
}

func (t *ServiceTracer) createMethodMap() (map[string][]*Method, error) {
	methodMap := map[string][]*Method{}
	for _, service := range t.cfg.Services {
		cachePath := filepath.Join(cacheDir, "maps", fmt.Sprintf("%s.yaml", service.Name))
		if _, err := os.Stat(cachePath); err == nil {
			file, err := ioutil.ReadFile(cachePath)
			if err != nil {
				return nil, xerrors.Errorf("failed to read maps cache: %w", err)
			}
			var cm map[string][]*Method
			if err := yaml.Unmarshal(file, &cm); err != nil {
				return nil, xerrors.Errorf("failed to unmarshal callmap file: %w", err)
			}
			for k, v := range cm {
				for _, mtd := range v {
					service, err := t.cfg.ServiceNameByGeneratedPath(mtd.GeneratedPath)
					if err != nil {
						return nil, xerrors.Errorf("failed to get service name: %w", err)
					}
					mtd.Service = service
				}
				methodMap[k] = v
			}
			continue
		}
		cm, err := t.analyzer.Analyze(service)
		if err != nil {
			return nil, xerrors.Errorf("failed to analyze: %w", err)
		}
		b, err := yaml.Marshal(cm)
		if err != nil {
			return nil, xerrors.Errorf("failed to marshal method map: %w", err)
		}
		if err := ioutil.WriteFile(cachePath, b, 0644); err != nil {
			return nil, xerrors.Errorf("failed to write method map file: %w", err)
		}
		for k, v := range cm {
			methodMap[k] = v
		}
	}
	return methodMap, nil
}
