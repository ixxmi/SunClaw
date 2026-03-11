package ops

import "testing"

func TestParseIntent(t *testing.T) {
	cases := []struct {
		in   string
		want Action
	}{
		{"help", ActionHelp},
		{"帮助一下", ActionHelp},
		{"查看状态", ActionStatus},
		{"status please", ActionStatus},
		{"重启服务", ActionRestart},
		{"restart now", ActionRestart},
		{"升级到最新", ActionUpgrade},
		{"deploy latest", ActionUpgrade},
		{"回滚到上个版本", ActionRollback},
		{"rollback prev", ActionRollback},
		{"random text", ActionUnknown},
	}
	for _, c := range cases {
		got := ParseIntent(c.in)
		if got != c.want {
			t.Fatalf("ParseIntent(%q)=%s, want %s", c.in, got, c.want)
		}
	}
}
