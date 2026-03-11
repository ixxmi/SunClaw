package ops

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubProcess struct {
	status  string
	version string
	restart string
	err     error
}

func (s stubProcess) Status(ctx context.Context) (string, error)  { return s.status, s.err }
func (s stubProcess) Version(ctx context.Context) (string, error) { return s.version, s.err }
func (s stubProcess) Restart(ctx context.Context) (string, error) {
	if s.restart != "" {
		return s.restart, s.err
	}
	return "restarted", s.err
}

type stubRelease struct {
	upgradeSteps []StepResult
	upgradeVer   string
	upgradeErr   error
	rollbackSteps []StepResult
	rollbackVer   string
	rollbackErr   error
	currentVer    string
}

func (s stubRelease) Upgrade(ctx context.Context, branch string) ([]StepResult, string, error) {
	return s.upgradeSteps, s.upgradeVer, s.upgradeErr
}

func (s stubRelease) Rollback(ctx context.Context, to string) ([]StepResult, string, error) {
	return s.rollbackSteps, s.rollbackVer, s.rollbackErr
}

func (s stubRelease) CurrentVersion(ctx context.Context) (string, error) {
	if s.currentVer != "" {
		return s.currentVer, nil
	}
	return s.upgradeVer, nil
}

type stubHealth struct {
	check HealthStatus
	retry HealthStatus
}

func (s stubHealth) Check(ctx context.Context) HealthStatus { return s.check }

func (s stubHealth) CheckWithRetry(ctx context.Context, interval time.Duration, successThreshold, failureThreshold int) HealthStatus {
	return s.retry
}

func baseCfg() Config {
	cfg := DefaultConfig()
	cfg.Security.AllowedRoles = nil
	cfg.Security.RequireConfirmFor = map[Action]bool{ActionUpgrade: true, ActionRollback: true}
	cfg.Timeouts.Health = 100 * time.Millisecond
	return cfg
}

func TestService_ConfirmRequired(t *testing.T) {
	cfg := baseCfg()
	s := NewService(cfg, stubProcess{}, stubRelease{}, stubHealth{}, NewGuard())

	_, err := s.Execute(context.Background(), Request{Action: ActionUpgrade, Confirm: false})
	if !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("upgrade expected confirm required, got %v", err)
	}
	_, err = s.Execute(context.Background(), Request{Action: ActionRollback, Confirm: false})
	if !errors.Is(err, ErrConfirmRequired) {
		t.Fatalf("rollback expected confirm required, got %v", err)
	}
}

func TestService_RestartUsesRetryHealth(t *testing.T) {
	cfg := baseCfg()
	h := stubHealth{
		check: HealthStatus{OK: false, Detail: "bad"},
		retry: HealthStatus{OK: true, Detail: "ok"},
	}
	s := NewService(cfg, stubProcess{version: "v1"}, stubRelease{}, h, NewGuard())

	resp, err := s.Execute(context.Background(), Request{Action: ActionRestart})
	if err != nil {
		t.Fatalf("restart expected success, got %v", err)
	}
	if resp.Health == nil || !resp.Health.OK {
		t.Fatalf("expected healthy response, got %#v", resp.Health)
	}
}

func TestService_ExecuteBasicFlows(t *testing.T) {
	cfg := baseCfg()
	s := NewService(
		cfg,
		stubProcess{status: "running", version: "abc123"},
		stubRelease{currentVer: "abc123"},
		stubHealth{check: HealthStatus{OK: true, Detail: "ok"}, retry: HealthStatus{OK: true, Detail: "ok"}},
		NewGuard(),
	)

	helpResp, err := s.Execute(context.Background(), Request{Command: "帮助"})
	if err != nil || helpResp.Action != ActionHelp || helpResp.Status != "ok" {
		t.Fatalf("help flow failed: resp=%+v err=%v", helpResp, err)
	}

	statusResp, err := s.Execute(context.Background(), Request{Action: ActionStatus})
	if err != nil || statusResp.Status != "ok" || statusResp.Version != "abc123" {
		t.Fatalf("status flow failed: resp=%+v err=%v", statusResp, err)
	}

	restartResp, err := s.Execute(context.Background(), Request{Action: ActionRestart})
	if err != nil || restartResp.Status != "ok" {
		t.Fatalf("restart flow failed: resp=%+v err=%v", restartResp, err)
	}

	failResp, err := s.Execute(context.Background(), Request{Action: Action("what")})
	if err == nil || failResp.Status != "failed" {
		t.Fatalf("expected failure on unknown action: resp=%+v err=%v", failResp, err)
	}
}
