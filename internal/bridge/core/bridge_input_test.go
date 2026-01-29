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
		{name: "internal_lf_to_cr", text: "a\nb", noNewline: false, want: "a\rb\r"},
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
