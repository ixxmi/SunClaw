package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrReleaseDisabled = errors.New("release management is disabled")

// ReleaseManager handles upgrade and rollback.
type ReleaseManager interface {
	Upgrade(ctx context.Context, branch string) ([]StepResult, string, error)
	Rollback(ctx context.Context, to string) ([]StepResult, string, error)
	CurrentVersion(ctx context.Context) (string, error)
}

type releaseManager struct {
	cfg    Config
	runner commandRunner
}

func NewReleaseManager(cfg Config, runner commandRunner) ReleaseManager {
	if runner == nil {
		runner = execRunner{}
	}
	return &releaseManager{cfg: cfg, runner: runner}
}

func (r *releaseManager) Upgrade(ctx context.Context, branch string) ([]StepResult, string, error) {
	if !r.cfg.Release.Enabled {
		return nil, "", ErrReleaseDisabled
	}
	if branch == "" {
		branch = r.cfg.Branch
	}
	steps := make([]StepResult, 0, 4)

	steps = append(steps, runStep(ctx, "git fetch", func(ctx context.Context) (string, error) {
		return r.runner.Run(ctx, r.cfg.ProjectDir, "git", "fetch", "origin")
	}))
	if !steps[len(steps)-1].Success {
		return steps, "", errors.New(steps[len(steps)-1].ErrMessage)
	}
	steps = append(steps, runStep(ctx, "git checkout", func(ctx context.Context) (string, error) {
		return r.runner.Run(ctx, r.cfg.ProjectDir, "git", "checkout", branch)
	}))
	if !steps[len(steps)-1].Success {
		return steps, "", errors.New(steps[len(steps)-1].ErrMessage)
	}
	steps = append(steps, runStep(ctx, "git pull", func(ctx context.Context) (string, error) {
		return r.runner.Run(ctx, r.cfg.ProjectDir, "git", "pull", "origin", branch)
	}))
	if !steps[len(steps)-1].Success {
		return steps, "", errors.New(steps[len(steps)-1].ErrMessage)
	}
	steps = append(steps, runStep(ctx, "go build", func(ctx context.Context) (string, error) {
		return r.runner.Run(ctx, r.cfg.ProjectDir, "go", "build", "./...")
	}))
	if !steps[len(steps)-1].Success {
		return steps, "", errors.New(steps[len(steps)-1].ErrMessage)
	}
	ver, _ := r.CurrentVersion(ctx)
	return steps, ver, nil
}

func (r *releaseManager) Rollback(ctx context.Context, to string) ([]StepResult, string, error) {
	if !r.cfg.Release.Enabled {
		return nil, "", ErrReleaseDisabled
	}
	if r.cfg.Release.ReleasesDir == "" || r.cfg.Release.CurrentLink == "" {
		return nil, "", errors.New("rollback requires releases_dir and current_link")
	}
	target, err := r.resolveRollbackTarget(to)
	if err != nil {
		return nil, "", err
	}
	steps := []StepResult{runStep(ctx, "rollback symlink", func(ctx context.Context) (string, error) {
		return "", switchSymlink(r.cfg.Release.CurrentLink, filepath.Join(r.cfg.Release.ReleasesDir, target))
	})}
	if !steps[0].Success {
		return steps, "", errors.New(steps[0].ErrMessage)
	}
	return steps, target, nil
}

func (r *releaseManager) CurrentVersion(ctx context.Context) (string, error) {
	if r.cfg.Release.CurrentLink != "" {
		resolved, err := filepath.EvalSymlinks(r.cfg.Release.CurrentLink)
		if err == nil {
			return filepath.Base(resolved), nil
		}
	}
	out, err := r.runner.Run(ctx, r.cfg.ProjectDir, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r *releaseManager) resolveRollbackTarget(to string) (string, error) {
	entries, err := os.ReadDir(r.cfg.Release.ReleasesDir)
	if err != nil {
		return "", err
	}
	list := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			list = append(list, e.Name())
		}
	}
	sort.Strings(list)
	if len(list) == 0 {
		return "", errors.New("no releases found")
	}
	if to != "" && to != "prev" {
		for _, v := range list {
			if v == to {
				return v, nil
			}
		}
		return "", fmt.Errorf("target release %s not found", to)
	}
	if len(list) < 2 {
		return "", errors.New("no previous release found")
	}
	return list[len(list)-2], nil
}

func switchSymlink(link, target string) error {
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

func releaseTag() string { return time.Now().Format("20060102150405") }
