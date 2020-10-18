package servicetracer

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"golang.org/x/xerrors"
)

type Config struct {
	Auth     Auth       `yaml:"auth"`
	Services []*Service `yaml:"services"`
}

func (c *Config) ServiceNameByGeneratedPath(path string) (string, error) {
	for _, service := range c.Services {
		mtds, err := service.Methods()
		if err != nil {
			return "", xerrors.Errorf("failed to get methods: %w", err)
		}
		if len(mtds) == 0 {
			continue
		}
		if mtds[0].GeneratedPath == path {
			return service.Name, nil
		}
	}
	return path, nil
}

func (c *Config) AuthToken() string {
	if c.Auth.Token.Env != "" {
		return os.Getenv(c.Auth.Token.Env)
	}
	return ""
}

type Auth struct {
	Token Token `yaml:"token"`
}

type Token struct {
	Env string `yaml:"env"`
}

type Service struct {
	Name  string    `yaml:"name"`
	Repo  string    `yaml:"repo"`
	Entry string    `yaml:"entry"`
	Proto Proto     `yaml:"proto"`
	mtds  []*Method `yaml:"-"`
}

func (s *Service) Methods() ([]*Method, error) {
	if s.mtds != nil {
		return s.mtds, nil
	}
	for _, path := range s.ProtoPaths() {
		mtds, err := ParseProto(s.Name, path)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse proto: %w", err)
		}
		s.mtds = append(s.mtds, mtds...)
	}
	return s.mtds, nil
}

func (s *Service) RepoName() string {
	paths := strings.Split(s.Repo, "/")
	return paths[len(paths)-1]
}

func (s *Service) ProtoRepoName() string {
	paths := strings.Split(s.Proto.Repo, "/")
	return paths[len(paths)-1]
}

func (s *Service) ProtoPaths() []string {
	paths := []string{}
	for _, path := range s.Proto.Path {
		paths = append(paths, filepath.Join(cacheDir, s.ProtoRepoName(), path))
	}
	return paths
}

type Proto struct {
	Repo string   `yaml:"repo"`
	Path []string `yaml:"path"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, xerrors.Errorf("failed to load config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(file, &cfg); err != nil {
		return nil, xerrors.Errorf("failed to unmarshal: %w", err)
	}
	return &cfg, nil
}
