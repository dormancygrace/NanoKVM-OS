package logs

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeEveryByte(t *testing.T) {
	for value := 0; value < 256; value++ {
		t.Run(fmt.Sprintf("%02x", value), func(t *testing.T) {
			input := append([]byte("safe:"), byte(value), 'x', '\n')
			middle := string(rune(value))
			switch {
			case value >= 0x80:
				middle = "\uFFFD" // A lone non-ASCII byte is invalid UTF-8.
			case value == '\n' || value == '\t':
			case value < 0x20 || value == 0x7f:
				middle = ""
			}
			want := "safe:" + middle + "x"
			got, lines, clipped := sanitizeTail(input, false)
			if got != want || lines != strings.Count(want, "\n")+1 || clipped {
				t.Fatalf("got %q, lines=%d, clipped=%v; want %q", got, lines, clipped, want)
			}
		})
	}
}

func TestSanitizeCharacterPaths(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"ascii", "safe printable line\twith tab\n", "safe printable line\twith tab"},
		{"unicode", "Журнал: café 😀\n", "Журнал: café 😀"},
		{"c1-controls", "a\u0085b\u009bc\n", "abc"},
		{"c1-reveals-secret", "to\u0085ken=do-not-show\n", redactedLine},
		{"format", "a\u200bb\u202ec\ufeffd\n", "abcd"},
		{"combining", "e\u0301\uFE0F\n", "e\u0301\uFE0F"},
		{"ansi", "\x1b[31mred\x1b[0m\n", "red"},
		{"osc", "\x1b]0;title\x07safe\n", "safe"},
		{"incomplete-escape", "safe\x1b[31\n", "safe[31"},
		{"invalid", "safe\xff\xfex\n", "safe\uFFFDx"},
		{"masked-ascii", "token=do-not-show\n", redactedLine},
		{"masked-controls", "to\u200bken=do-not-show\n", redactedLine},
		{"masked-ansi", "\x1b[31mpassword=do-not-show\x1b[0m\n", redactedLine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, lines, clipped := sanitizeTail([]byte(tc.input), false)
			if got != tc.want || lines != 1 || clipped {
				t.Fatalf("got %q, lines=%d, clipped=%v; want %q", got, lines, clipped, tc.want)
			}
		})
	}
}
