package servicetracer

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"golang.org/x/xerrors"
)

type Option struct {
	Config string `description:"specify config path" short:"c" long:"config" required:"true"`
	Output string `description:"specify output name" short:"o" long:"output" default:"trace"`
}

type Config struct {
	Auth     Auth       `yaml:"auth"`
	Services []*Service `yaml:"services"`
	Output   string     `yaml:"-"`
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
		for _, mtd := range mtds {
			if mtd.GeneratedPath == path {
				return service.Name, nil
			}
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

var (
	testPattern = regexp.MustCompile(`_test\.go$`)
)

func (s *Service) existsMainPackage(path string) bool {
	var fset token.FileSet
	pkgs, err := parser.ParseDir(&fset, path, nil, 0)
	if err != nil {
		return false
	}
	return pkgs["main"] != nil
}

func (s *Service) containsGoFile(dir string) bool {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return false
	}
	for _, path := range files {
		file := filepath.Base(path)
		if strings.HasPrefix(file, ".") {
			continue
		}
		if strings.HasPrefix(file, "#") {
			continue
		}
		if strings.HasPrefix(file, "~") {
			continue
		}
		if testPattern.MatchString(file) {
			continue
		}
		return true
	}
	return false
}

func (s *Service) Entries() ([]string, error) {
	root := RepoRoot(s)
	if s.Entry != "" {
		return []string{filepath.Join(root, s.Entry)}, nil
	}
	paths := []string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return xerrors.Errorf("failed to walk: %w", err)
		}
		if !info.IsDir() {
			return nil
		}
		if info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !s.containsGoFile(path) {
			return nil
		}
		if !s.existsMainPackage(path) {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return nil, xerrors.Errorf("failed to walk: %w", err)
	}
	return paths, nil
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

func (s *Service) MethodNameMap() (map[string][]*Method, error) {
	mtds, err := s.Methods()
	if err != nil {
		return nil, xerrors.Errorf("failed to get methods: %w", err)
	}
	nameMap := map[string][]*Method{}
	for _, mtd := range mtds {
		nameMap[mtd.Name] = append(nameMap[mtd.Name], mtd)
	}
	return nameMap, nil
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
		paths = append(paths, filepath.Join(ProtoRepoRoot(s), path))
	}
	return paths
}

type Proto struct {
	Repo string   `yaml:"repo"`
	Path []string `yaml:"path"`
}

type Method struct {
	Pkg           string `yaml:"pkg"`
	GeneratedPath string `yaml:"generated_path"`
	Service       string `yaml:"service"`
	Name          string `yaml:"name"`
	InputType     string `yaml:"input_type"`
	OutputType    string `yaml:"output_type"`
}

func (m *Method) GeneratedPathToRepo() string {
	// GeneratedPath starts with like github.com/org/repo/a/b/c...
	paths := strings.Split(m.GeneratedPath, "/")
	return strings.Join(paths[:3], "/")
}

func (m *Method) MangledName() string {
	return strings.ToLower(fmt.Sprintf("%s.%s.%s", m.Name, m.InputType, m.OutputType))
}

func LoadConfig(opt *Option) (*Config, error) {
	file, err := ioutil.ReadFile(opt.Config)
	if err != nil {
		return nil, xerrors.Errorf("failed to load config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(file, &cfg); err != nil {
		return nil, xerrors.Errorf("failed to unmarshal: %w", err)
	}
	cfg.Output = opt.Output
	return &cfg, nil
}
