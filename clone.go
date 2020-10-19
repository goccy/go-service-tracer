package servicetracer

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	"golang.org/x/xerrors"
)

func cloneURL(token, repo string) string {
	if token != "" {
		return fmt.Sprintf("https://%s@%s.git", token, repo)
	}
	return fmt.Sprintf("https://%s.git", repo)
}

func clone(cloneDir, repo, url string) error {
	if _, err := os.Stat(cloneDir); err == nil {
		return nil
	}
	fmt.Printf("cloning %s...\n", repo)
	if _, err := git.PlainClone(cloneDir, false, &git.CloneOptions{URL: url, Progress: os.Stdout}); err != nil {
		return xerrors.Errorf("failed to clone repository %s: %w", url, err)
	}
	return nil
}

func CloneRepository(cfg *Config) error {
	token := cfg.AuthToken()
	for _, service := range cfg.Services {
		if err := clone(RepoRoot(service), service.Repo, cloneURL(token, service.Repo)); err != nil {
			return xerrors.Errorf("failed to clone repository %s: %w", service.Repo, err)
		}
		if err := clone(ProtoRepoRoot(service), service.Proto.Repo, cloneURL(token, service.Proto.Repo)); err != nil {
			return xerrors.Errorf("failed to clone repository %s: %w", service.Proto.Repo, err)
		}
	}
	return nil
}
