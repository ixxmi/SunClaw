package ops

import (
	"context"
	"errors"
	"fmt"
)

// Service orchestrates ops actions.
type Service struct {
	cfg     Config
	process ProcessController
	release ReleaseManager
	health  HealthChecker
	guard   *Guard
}

func NewService(cfg Config, process ProcessController, release ReleaseManager, health HealthChecker, guard *Guard) *Service {
	if guard == nil {
		guard = NewGuard()
	}
	if process == nil {
		process = NewProcessController(cfg)
	}
	if release == nil {
		release = NewReleaseManager(cfg, nil)
	}
	if health == nil {
		health = NewHealthChecker(cfg.HealthCheck)
	}
	return &Service{cfg: cfg, process: process, release: release, health: health, guard: guard}
}

func (s *Service) Execute(ctx context.Context, req Request) (Response, error) {
	act := req.Action
	if act == "" || act == ActionUnknown {
		act = ParseIntent(req.Command)
	}
	if act == ActionUnknown {
		return Response{Status: "failed", Action: ActionUnknown, Message: "unknown action, try help"}, fmt.Errorf("unknown action")
	}

	if len(s.cfg.Security.AllowedRoles) > 0 && !s.cfg.Security.AllowedRoles[req.Role] {
		return Response{Status: "failed", Action: act, Message: "permission denied"}, ErrPermissionDenied
	}
	if s.cfg.Security.RequireConfirmFor[act] && !req.Confirm {
		return Response{Status: "failed", Action: act, Message: "confirmation required"}, ErrConfirmRequired
	}

	cached, err := s.guard.Begin(req.RequestID)
	if err != nil {
		return Response{Status: "failed", Action: act, Message: err.Error()}, err
	}
	if cached != nil {
		return *cached, nil
	}

	resp := Response{Status: "ok", Action: act, RequestID: req.RequestID}
	defer func() { s.guard.End(req.RequestID, resp) }()

	switch act {
	case ActionHelp:
		resp.Message = "supported actions: help, status, restart, upgrade, rollback"
		return resp, nil
	case ActionStatus:
		if s.process != nil {
			st := runStep(ctx, "process status", func(ctx context.Context) (string, error) {
				return s.process.Status(ctx)
			})
			resp.Steps = append(resp.Steps, st)
			if !st.Success {
				resp.Status = "failed"
				resp.Message = st.ErrMessage
				return resp, errors.New(st.ErrMessage)
			}
		}
		if s.release != nil {
			v, _ := s.release.CurrentVersion(ctx)
			resp.Version = v
		}
		hs := s.health.Check(ctx)
		resp.Health = &hs
		if !hs.OK {
			resp.Status = "failed"
			resp.Message = hs.Detail
			return resp, errors.New(hs.Detail)
		}
		resp.Message = "service is healthy"
		return resp, nil
	case ActionRestart:
		st := runStep(ctx, "restart", func(ctx context.Context) (string, error) {
			return s.process.Restart(ctx)
		})
		resp.Steps = append(resp.Steps, st)
		if !st.Success {
			resp.Status = "failed"
			resp.Message = st.ErrMessage
			return resp, errors.New(st.ErrMessage)
		}
		hs := s.runHealthRetry(ctx)
		resp.Health = &hs
		if !hs.OK {
			resp.Status = "failed"
			resp.Message = hs.Detail
			return resp, errors.New(hs.Detail)
		}
		resp.Message = "restart succeeded"
		return resp, nil
	case ActionUpgrade:
		steps, ver, uerr := s.release.Upgrade(ctx, req.Branch)
		resp.Steps = append(resp.Steps, steps...)
		resp.Version = ver
		if uerr != nil {
			if s.cfg.Release.AutoRollback {
				rSteps, rVer, rErr := s.release.Rollback(ctx, "prev")
				resp.Steps = append(resp.Steps, rSteps...)
				if rErr == nil {
					resp.Version = rVer
					resp.Message = "upgrade failed; auto rollback succeeded"
				} else {
					resp.Message = fmt.Sprintf("upgrade failed; auto rollback failed: %v", rErr)
				}
			}
			resp.Status = "failed"
			if resp.Message == "" {
				resp.Message = uerr.Error()
			}
			return resp, uerr
		}
		hs := s.runHealthRetry(ctx)
		resp.Health = &hs
		if !hs.OK {
			resp.Status = "failed"
			resp.Message = hs.Detail
			return resp, errors.New(hs.Detail)
		}
		resp.Message = "upgrade succeeded"
		return resp, nil
	case ActionRollback:
		steps, ver, rerr := s.release.Rollback(ctx, req.RollbackTo)
		resp.Steps = append(resp.Steps, steps...)
		resp.Version = ver
		if rerr != nil {
			resp.Status = "failed"
			resp.Message = rerr.Error()
			return resp, rerr
		}
		hs := s.runHealthRetry(ctx)
		resp.Health = &hs
		if !hs.OK {
			resp.Status = "failed"
			resp.Message = hs.Detail
			return resp, errors.New(hs.Detail)
		}
		resp.Message = "rollback succeeded"
		return resp, nil
	default:
		return Response{Status: "failed", Action: act, Message: "unsupported action"}, fmt.Errorf("unsupported action %s", act)
	}
}

func (s *Service) runHealthRetry(ctx context.Context) HealthStatus {
	hctx, cancel := context.WithTimeout(ctx, s.cfg.Timeouts.Health)
	defer cancel()
	return s.health.CheckWithRetry(hctx, s.cfg.HealthCheck.Interval, s.cfg.HealthCheck.SuccessThreshold, s.cfg.HealthCheck.FailureThreshold)
}
