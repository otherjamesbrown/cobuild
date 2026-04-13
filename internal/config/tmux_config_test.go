package config

import (
	"reflect"
	"testing"
)

func TestResolveTmuxSession(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.ResolveTmuxSession("demo"); got != "cobuild-demo" {
		t.Fatalf("ResolveTmuxSession() = %q, want cobuild-demo", got)
	}

	cfg.Dispatch.TmuxSession = "custom-session"
	if got := cfg.ResolveTmuxSession("demo"); got != "custom-session" {
		t.Fatalf("ResolveTmuxSession() with override = %q, want custom-session", got)
	}
}

func TestTmuxArgs(t *testing.T) {
	cfg := DefaultConfig()
	args := []string{"list-sessions", "-F", "#{session_name}"}
	if got := cfg.TmuxArgs(args...); !reflect.DeepEqual(got, args) {
		t.Fatalf("TmuxArgs() without socket = %#v, want %#v", got, args)
	}

	cfg.Dispatch.TmuxSocket = "/tmp/cobuild-test.sock"
	want := []string{"-S", "/tmp/cobuild-test.sock", "list-sessions", "-F", "#{session_name}"}
	if got := cfg.TmuxArgs(args...); !reflect.DeepEqual(got, want) {
		t.Fatalf("TmuxArgs() with socket = %#v, want %#v", got, want)
	}
}
