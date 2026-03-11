package ops

import (
	"errors"
	"time"
)

// Config is ops module configuration.
type Config struct {
	ServiceName string
	Mode        string // auto|systemd|launchd|windows|supervisor|native
	ProjectDir  string
	Branch      string

	Timeouts    Timeouts
	HealthCheck HealthCheckConfig
	Release     ReleaseConfig
	Security    SecurityConfig
}

type Timeouts struct {
	Restart time.Duration
	Upgrade time.Duration
	Health  time.Duration
}

type HealthCheckConfig struct {
	Type             string // http|tcp
	URL              string
	TCPAddress       string
	Interval         time.Duration
	SuccessThreshold int
	FailureThreshold int
}

type ReleaseConfig struct {
	Enabled      bool
	ReleasesDir  string
	CurrentLink  string
	KeepLast     int
	AutoRollback bool
}

type SecurityConfig struct {
	RequireConfirmFor map[Action]bool
	AllowedRoles      map[string]bool
}

func DefaultConfig() Config {
	return Config{
		ServiceName: "goclaw",
		Mode:        "auto",
		ProjectDir:  ".",
		Branch:      "main",
		Timeouts: Timeouts{Restart: 60 * time.Second, Upgrade: 10 * time.Minute, Health: 30 * time.Second},
		HealthCheck: HealthCheckConfig{Type: "http", URL: "http://127.0.0.1:8080/health", Interval: 2 * time.Second, SuccessThreshold: 1, FailureThreshold: 3},
		Release: ReleaseConfig{Enabled: true, ReleasesDir: "/opt/goclaw/releases", CurrentLink: "/opt/goclaw/current", KeepLast: 5, AutoRollback: true},
		Security: SecurityConfig{RequireConfirmFor: map[Action]bool{ActionUpgrade: true, ActionRollback: true}, AllowedRoles: map[string]bool{"admin": true, "operator": true}},
	}
}

func (c Config) Validate() error {
	if c.ServiceName == "" {
		return errors.New("ops.service_name is required")
	}
	switch c.Mode {
	case "auto", "systemd", "launchd", "windows", "supervisor", "native":
	default:
		return errors.New("ops.mode must be one of: auto|systemd|launchd|windows|supervisor|native")
	}
	if c.ProjectDir == "" {
		return errors.New("ops.project_dir is required")
	}
	if c.Timeouts.Restart <= 0 || c.Timeouts.Upgrade <= 0 || c.Timeouts.Health <= 0 {
		return errors.New("ops.timeouts must be positive")
	}
	if c.HealthCheck.Interval <= 0 {
		return errors.New("ops.healthcheck.interval must be positive")
	}
	if c.HealthCheck.SuccessThreshold <= 0 || c.HealthCheck.FailureThreshold <= 0 {
		return errors.New("ops.healthcheck thresholds must be positive")
	}
	return nil
}
