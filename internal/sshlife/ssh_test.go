package sshlife

import "testing"

func TestWinToCygwin(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`C:\xpc\agent.py`:    "/cygdrive/c/xpc/agent.py",
		`D:\foo bar\baz.txt`: "/cygdrive/d/foo bar/baz.txt",
		`/already/posix`:     "/already/posix",
		`bare\path\file`:     "bare/path/file",
		`C:\`:                "/cygdrive/c/",
	}
	for in, want := range cases {
		if got := winToCygwin(in); got != want {
			t.Errorf("winToCygwin(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCygDir(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/cygdrive/c/xpc/agent.py": "/cygdrive/c/xpc",
		"/foo":                     "",
		"":                         "",
		"a/b/c":                    "a/b",
	}
	for in, want := range cases {
		if got := cygDir(in); got != want {
			t.Errorf("cygDir(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestShellQEscapesQuotes(t *testing.T) {
	t.Parallel()
	if got := shellQ("o'malley"); got != `'o'\''malley'` {
		t.Errorf("shellQ embedding single-quote: got %q", got)
	}
}
