package macnotify

import (
	"errors"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		`plain`:          `"plain"`,
		`say "hi"`:       `"say \"hi\""`,
		`back\slash`:     `"back\\slash"`,
		`\" injection "`: `"\\\" injection \""`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

type fake struct {
	hasNotifier bool
	failing     map[string]bool
	calls       []string
}

func (f *fake) sender() sender {
	return sender{
		lookPath: func(file string) (string, error) {
			if f.hasNotifier {
				return "/bin/" + file, nil
			}
			return "", errors.New("not found")
		},
		run: func(name string, args ...string) error {
			f.calls = append(f.calls, name+" "+strings.Join(args, " "))
			if f.failing[name] {
				return errors.New("failed")
			}
			return nil
		},
	}
}

func TestPrefersTerminalNotifier(t *testing.T) {
	f := &fake{hasNotifier: true}
	if err := f.sender().send("T", "M", "https://x", "key:1"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != "/bin/terminal-notifier -title T -message M -group key:1 -open https://x" {
		t.Errorf("calls = %q", f.calls)
	}
}

func TestFallsBackToOsascript(t *testing.T) {
	f := &fake{hasNotifier: true, failing: map[string]bool{"/bin/terminal-notifier": true}}
	if err := f.sender().send("T", `say "hi"`, "", "g"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[1] != `osascript -e display notification "say \"hi\"" with title "T"` {
		t.Errorf("calls = %q", f.calls)
	}

	f = &fake{}
	_ = f.sender().send("T", "M", "", "g")
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], "osascript") {
		t.Errorf("without terminal-notifier: calls = %q", f.calls)
	}

	f = &fake{failing: map[string]bool{"osascript": true}}
	if err := f.sender().send("T", "M", "", "g"); err == nil {
		t.Error("want error when every way fails")
	}
}
