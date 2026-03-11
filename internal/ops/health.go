package ops

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HealthChecker probes service health.
type HealthChecker interface {
	Check(ctx context.Context) HealthStatus
	CheckWithRetry(ctx context.Context, interval time.Duration, successThreshold, failureThreshold int) HealthStatus
}

type healthChecker struct{ cfg HealthCheckConfig }

func NewHealthChecker(cfg HealthCheckConfig) HealthChecker { return &healthChecker{cfg: cfg} }

func (h *healthChecker) Check(ctx context.Context) HealthStatus {
	var err error
	switch h.cfg.Type {
	case "", "http":
		err = h.checkHTTP(ctx)
	case "tcp":
		err = h.checkTCP(ctx)
	default:
		err = fmt.Errorf("unsupported health check type: %s", h.cfg.Type)
	}
	if err != nil {
		return HealthStatus{OK: false, Detail: err.Error(), Checked: time.Now()}
	}
	return HealthStatus{OK: true, Detail: "ok", Checked: time.Now()}
}

func (h *healthChecker) CheckWithRetry(ctx context.Context, interval time.Duration, successThreshold, failureThreshold int) HealthStatus {
	if interval <= 0 {
		interval = time.Second
	}
	if successThreshold <= 0 {
		successThreshold = 1
	}
	if failureThreshold <= 0 {
		failureThreshold = 1
	}
	succ, fail := 0, 0
	last := HealthStatus{OK: false, Detail: "not checked", Checked: time.Now()}
	for {
		select {
		case <-ctx.Done():
			last.OK = false
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				last.Detail = "health check timeout"
			} else {
				last.Detail = ctx.Err().Error()
			}
			last.Checked = time.Now()
			return last
		default:
		}
		last = h.Check(ctx)
		if last.OK {
			succ++
			fail = 0
			if succ >= successThreshold {
				return last
			}
		} else {
			fail++
			succ = 0
			if fail >= failureThreshold {
				return last
			}
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			last.OK = false
			last.Detail = ctx.Err().Error()
			last.Checked = time.Now()
			return last
		case <-t.C:
		}
	}
}

func (h *healthChecker) checkHTTP(ctx context.Context) error {
	if h.cfg.URL == "" {
		return errors.New("healthcheck.url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health http status %d", resp.StatusCode)
	}
	return nil
}

func (h *healthChecker) checkTCP(ctx context.Context) error {
	if h.cfg.TCPAddress == "" {
		return errors.New("healthcheck.tcp_address is required")
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", h.cfg.TCPAddress)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
