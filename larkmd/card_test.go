package larkmd

import "testing"

func TestCardForm(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"* [Done]\n* [Done]", "* :DONE:\n* :DONE:", true},
		{"1. [OK] two", "1. :OK: two", true},
		{"intro [Done]\n- item [Done]", "intro :DONE:\n- item :DONE:", true},
		{"* plain", "", false},
		{"[Done] on a line of words", "", false},
		{"- [Nope] unknown", "", false},
		{"- [Done](https://x.y) link", "", false},
		{"- `[Done]` code", "", false},
		{"```\n- [Done]\n```", "", false},
		{"- [Done] ![img](k)", "", false},
		{"- [Done] <at user_id=\"ou_1\">a</at>", "", false},
	} {
		got, ok := CardForm(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("CardForm(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
