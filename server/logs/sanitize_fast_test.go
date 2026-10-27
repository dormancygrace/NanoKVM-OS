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

func BenchmarkSanitizeCharacterPaths(b *testing.B) {
	for _, tc := range []struct{ name, line string }{
		{"ASCII", "[info] capture ready at 2560x1440, 60 frames per second\n"},
		{"Unicode", "[info] Получен адрес сети; видео работает 😀\n"},
		{"Controls", "\x1b[32m[info]\x1b[0m capture ready \u200b video\n"},
	} {
		for _, size := range []int{64, 1024, 65536, MaxBytes} {
			b.Run(fmt.Sprintf("%s/%d", tc.name, size), func(b *testing.B) {
				data := []byte(strings.Repeat(tc.line, (size+len(tc.line)-1)/len(tc.line))[:size])
				data[len(data)-1] = '\n'
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					sanitizeTail(data, false)
				}
			})
		}
	}
}
