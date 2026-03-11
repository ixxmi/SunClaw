package ops

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

type recordRunner struct {
	calls []runnerCall
	errOn map[string]error
}

type runnerCall struct {
	name string
	args []string
}

func (r *recordRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	if r.errOn != nil {
		if err, ok := r.errOn[name+" "+firstArg(args)]; ok {
			return "", err
		}
	}
	return "ok", nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func TestResolveProcessMode_Auto(t *testing.T) {
	want := "native"
	switch runtime.GOOS {
	case "linux":
		want = "systemd"
	case "darwin":
		want = "launchd"
	case "windows":
		want = "windows"
	}
	if got := resolveProcessMode("auto", runtime.GOOS); got != want {
		t.Fatalf("resolveProcessMode(auto,%s)=%s, want=%s", runtime.GOOS, got, want)
	}
}

func TestResolveProcessMode_ExplicitOverride(t *testing.T) {
	if got := resolveProcessMode("systemd", "darwin"); got != "systemd" {
		t.Fatalf("explicit mode should override goos, got %s", got)
	}
}

func TestProcessController_SystemdCommands(t *testing.T) {
	r := &recordRunner{}
	p := NewProcessController(Config{Mode: "systemd", ServiceName: "svc"})
	p.runner = r

	_, _ = p.Restart(context.Background())
	_, _ = p.Status(context.Background())

	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.calls))
	}
	if r.calls[0].name != "systemctl" || r.calls[0].args[0] != "restart" || r.calls[0].args[1] != "svc" {
		t.Fatalf("unexpected restart call: %#v", r.calls[0])
	}
	if r.calls[1].name != "systemctl" || r.calls[1].args[0] != "status" || r.calls[1].args[1] != "svc" {
		t.Fatalf("unexpected status call: %#v", r.calls[1])
	}
}

func TestProcessController_LaunchdCommands(t *testing.T) {
	r := &recordRunner{}
	p := NewProcessController(Config{Mode: "launchd", ServiceName: "com.demo.goclaw"})
	p.runner = r

	_, _ = p.Restart(context.Background())

	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	if r.calls[0].name != "launchctl" {
		t.Fatalf("unexpected command: %#v", r.calls[0])
	}
	wantArgs := []string{"kickstart", "-k", "system/com.demo.goclaw"}
	for i := range wantArgs {
		if r.calls[0].args[i] != wantArgs[i] {
			t.Fatalf("unexpected launchd args: %#v", r.calls[0].args)
		}
	}
}

func TestProcessController_WindowsCommands(t *testing.T) {
	r := &recordRunner{}
	p := NewProcessController(Config{Mode: "windows", ServiceName: "GoClaw"})
	p.runner = r

	_, _ = p.Restart(context.Background())
	_, _ = p.Status(context.Background())

	if len(r.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(r.calls))
	}
	if r.calls[0].name != "sc" || r.calls[0].args[0] != "stop" {
		t.Fatalf("unexpected stop call: %#v", r.calls[0])
	}
	if r.calls[1].name != "sc" || r.calls[1].args[0] != "start" {
		t.Fatalf("unexpected start call: %#v", r.calls[1])
	}
	if r.calls[2].name != "sc" || r.calls[2].args[0] != "query" {
		t.Fatalf("unexpected query call: %#v", r.calls[2])
	}
}

func TestProcessController_NativeFallback(t *testing.T) {
	p := NewProcessController(Config{Mode: "native", ServiceName: "svc"})
	msg, err := p.Restart(context.Background())
	if err != nil {
		t.Fatalf("native restart should not fail, got %v", err)
	}
	if msg == "" {
		t.Fatal("expected native fallback message")
	}
}

func TestIsAllowedCommand_ExtendedWhitelist(t *testing.T) {
	cases := []struct {
		name string
		args []string
		ok   bool
	}{
		{name: "launchctl", args: []string{"kickstart", "-k", "system/x"}, ok: true},
		{name: "sc", args: []string{"query", "svc"}, ok: true},
		{name: "sc", args: []string{"delete", "svc"}, ok: false},
		{name: "launchctl", args: []string{"kickstart", "&&", "x"}, ok: false},
	}
	for _, c := range cases {
		if got := isAllowedCommand(c.name, c.args); got != c.ok {
			t.Fatalf("isAllowedCommand(%s,%v)=%v, want %v", c.name, c.args, got, c.ok)
		}
	}
}

func TestProcessController_WindowsStopError(t *testing.T) {
	r := &recordRunner{errOn: map[string]error{"sc stop": errors.New("stop failed")}}
	p := NewProcessController(Config{Mode: "windows", ServiceName: "svc"})
	p.runner = r
	_, err := p.Restart(context.Background())
	if err == nil {
		t.Fatal("expected error when stop fails")
	}
}
