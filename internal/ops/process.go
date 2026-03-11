package ops

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ProcessController controls runtime process lifecycle.
type ProcessController interface {
	Status(ctx context.Context) (string, error)
	Version(ctx context.Context) (string, error)
	Restart(ctx context.Context) (string, error)
}

type commandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if !isAllowedCommand(name, args) {
		return "", fmt.Errorf("command not allowed: %s %s", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

type ShellProcessController struct {
	cfg    Config
	mode   string
	runner commandRunner
}

func NewProcessController(cfg Config) *ShellProcessController {
	return &ShellProcessController{
		cfg:    cfg,
		mode:   resolveProcessMode(cfg.Mode, runtime.GOOS),
		runner: execRunner{},
	}
}

func (p *ShellProcessController) Status(ctx context.Context) (string, error) {
	switch p.mode {
	case "systemd":
		return p.runner.Run(ctx, "", "systemctl", "status", p.cfg.ServiceName)
	case "supervisor":
		return p.runner.Run(ctx, "", "supervisorctl", "status", p.cfg.ServiceName)
	case "launchd":
		return fmt.Sprintf("launchd status is limited, try: launchctl print %s", launchdLabel(p.cfg.ServiceName)), nil
	case "windows":
		return p.runner.Run(ctx, "", "sc", "query", p.cfg.ServiceName)
	default:
		return fmt.Sprintf("service %s mode=%s", p.cfg.ServiceName, p.mode), nil
	}
}

func (p *ShellProcessController) Version(ctx context.Context) (string, error) {
	return p.runner.Run(ctx, p.cfg.ProjectDir, "git", "rev-parse", "--short", "HEAD")
}

func (p *ShellProcessController) Restart(ctx context.Context) (string, error) {
	switch p.mode {
	case "systemd":
		return p.runner.Run(ctx, "", "systemctl", "restart", p.cfg.ServiceName)
	case "supervisor":
		return p.runner.Run(ctx, "", "supervisorctl", "restart", p.cfg.ServiceName)
	case "launchd":
		return p.runner.Run(ctx, "", "launchctl", "kickstart", "-k", launchdLabel(p.cfg.ServiceName))
	case "windows":
		stopOut, stopErr := p.runner.Run(ctx, "", "sc", "stop", p.cfg.ServiceName)
		if stopErr != nil {
			return stopOut, stopErr
		}
		startOut, startErr := p.runner.Run(ctx, "", "sc", "start", p.cfg.ServiceName)
		if startErr != nil {
			return strings.TrimSpace(stopOut + "\n" + startOut), startErr
		}
		return strings.TrimSpace(stopOut + "\n" + startOut), nil
	default:
		return fmt.Sprintf("restart noop in %s mode", p.mode), nil
	}
}

func resolveProcessMode(mode string, goos string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" || m == "auto" {
		switch goos {
		case "linux":
			return "systemd"
		case "darwin":
			return "launchd"
		case "windows":
			return "windows"
		default:
			return "native"
		}
	}
	return m
}

func launchdLabel(service string) string {
	if strings.HasPrefix(service, "system/") || strings.HasPrefix(service, "gui/") {
		return service
	}
	return "system/" + service
}

func isAllowedCommand(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	if hasUnsafeArgs(args) {
		return false
	}

	if name == "git" {
		switch args[0] {
		case "rev-parse", "fetch", "checkout", "pull":
			return true
		default:
			return false
		}
	}

	if name == "go" {
		return args[0] == "build"
	}

	if name == "systemctl" || name == "supervisorctl" {
		if len(args) < 2 {
			return false
		}
		return (args[0] == "restart" || args[0] == "status") && args[1] != ""
	}

	if name == "launchctl" {
		switch args[0] {
		case "kickstart":
			return len(args) >= 3 && args[1] == "-k" && args[2] != ""
		case "stop", "start":
			return len(args) >= 2 && args[1] != ""
		default:
			return false
		}
	}

	if name == "sc" || name == "sc.exe" {
		if len(args) < 2 {
			return false
		}
		switch strings.ToLower(args[0]) {
		case "stop", "start", "query":
			return args[1] != ""
		default:
			return false
		}
	}

	return false
}

func hasUnsafeArgs(args []string) bool {
	for _, a := range args {
		if strings.ContainsAny(a, ";&|`$") {
			return true
		}
	}
	return false
}
