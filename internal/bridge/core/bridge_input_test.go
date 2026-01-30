package core

import "testing"

func TestNormalizePTYInput(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		noNewline bool
		want      string
	}{
		{name: "append_cr", text: "hello", noNewline: false, want: "hello\r"},
		{name: "lf_to_cr", text: "hello\n", noNewline: false, want: "hello\r"},
		{name: "crlf_to_cr", text: "hello\r\n", noNewline: false, want: "hello\r"},
		{name: "internal_lf_preserved", text: "a\nb", noNewline: false, want: "a\nb\r"},
		{name: "no_newline_kept", text: "hello\n", noNewline: true, want: "hello\n"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := string(normalizePTYInput(tc.text, tc.noNewline))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeKittyPTYInput(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		noNewline bool
		want      string
	}{
		{name: "append_enter_csi_u", text: "hi", noNewline: false, want: "hi\x1b[13;1u"},
		{name: "trim_trailing_newlines", text: "hi\n", noNewline: false, want: "hi\x1b[13;1u"},
		{name: "no_newline_kept", text: "hi\n", noNewline: true, want: "hi\n"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := string(normalizeKittyPTYInput(tc.text, tc.noNewline))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestObservePTYOutputKittyKeyboard(t *testing.T) {
	b, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}

	if b.isKittyKeyboardEnabled() {
		t.Fatalf("expected kitty keyboard disabled initially")
	}

	b.observePTYOutput("\x1b[>1u")
	if !b.isKittyKeyboardEnabled() {
		t.Fatalf("expected kitty keyboard enabled after CSI >1u")
	}

	b.observePTYOutput("\x1b[<u")
	if b.isKittyKeyboardEnabled() {
		t.Fatalf("expected kitty keyboard disabled after CSI <u")
	}
}
