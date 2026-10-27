package logs

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSensitiveInputs(b *testing.B) {
	for _, n := range []int{8, 16, 32, 64, 95, 128, 129, 256, 257, 1024} {
		for _, pattern := range []string{"a", "pxk", "paaaaaad", "passwoxd", "private_kex", "regular"} {
			body := strings.Repeat(pattern, (n+len(pattern)-1)/len(pattern))[:n]
			if pattern == "regular" {
				line := "[info] capture ready at 2560x1440, 60 frames per second"
				body = strings.Repeat(line, (n+len(line)-1)/len(line))[:n]
			}
			b.Run(fmt.Sprintf("%d/%s", n, pattern), func(b *testing.B) {
				for b.Loop() {
					sensitiveLine(body)
				}
			})
		}
	}
}
func BenchmarkSanitizeRepetitive(b *testing.B) {
	for _, pattern := range []string{"a", "pxk", "paaaaaad", "passwoxd", "private_kex"} {
		line := strings.Repeat(pattern, (95+len(pattern)-1)/len(pattern))[:95] + "\n"
		data := []byte(strings.Repeat(line, MaxBytes/len(line)))
		b.Run(pattern, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				sanitizeTail(data, false)
			}
		})
	}
}
func TestRoutedSensitiveEquivalent(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 95, 127, 128, 129, 255, 256, 257, 1024, 4096} {
		for _, word := range sensitiveWords {
			altered := []byte(word)
			altered[len(word)/2] ^= 0x10
			for _, pattern := range []string{"a", "pxk", "paaaaaad", string(altered)} {
				body := strings.Repeat(pattern, (n+len(pattern)-1)/len(pattern))[:n]
				for _, s := range []string{body, body + word, word + body, "[info] " + body, "[info] " + body + word} {
					if sensitiveLine(s) != referenceSensitiveLine(s) {
						t.Fatalf("n%d word%q input%q", n, word, s)
					}
				}
			}
		}
	}
}
