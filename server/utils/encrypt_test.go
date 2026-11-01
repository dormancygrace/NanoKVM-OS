package utils

import (
	"net/url"
	"testing"
)

const opensslCiphertextWithPlus = "U2FsdGVkX18zLUxaLNGy7jL96oMO4tq6wDYwVzUMO3XfTY2Zy/ipO4LDEqtBT+fx"

func TestDecryptRejectsMalformedCiphertextWithoutPanic(t *testing.T) {
	for _, ciphertext := range []string{
		"not base64!", "U2FsdGVkX18=", "U2FsdGVkX18AAAAAAAAAAA==",
		"U2FsdGVkX18AAAAAAAAAAAAAAAAAAAAA", "%zz",
	} {
		t.Run(ciphertext, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Decrypt panicked: %v", recovered)
				}
			}()
			if _, err := DecodeDecrypt(ciphertext); err == nil {
				t.Fatal("DecodeDecrypt accepted malformed ciphertext")
			}
		})
	}
}

func TestCredentialPasswordFormats(t *testing.T) {
	for _, value := range []string{"päss+%20密碼", "U2FsdGVkX1-not-ciphertext", opensslCiphertextWithPlus, "", " white space "} {
		got, err := DecodeCredentialPassword(value, "plain")
		if err != nil || got != value {
			t.Fatalf("plain value was transformed: %q %v", got, err)
		}
	}
	for _, encoding := range []string{"", "cryptojs"} {
		for _, value := range []string{opensslCiphertextWithPlus, url.QueryEscape(opensslCiphertextWithPlus)} {
			got, err := DecodeCredentialPassword(value, encoding)
			if err != nil || got != "operator-password" {
				t.Fatalf("legacy value rejected: %q %v", got, err)
			}
		}
		if _, err := DecodeCredentialPassword("plaintext-must-not-be-guessed", encoding); err == nil {
			t.Fatal("implicit plaintext accepted")
		}
	}
	for _, encoding := range []string{"PLAIN", "plain ", "aes", "future"} {
		if _, err := DecodeCredentialPassword(opensslCiphertextWithPlus, encoding); err == nil {
			t.Fatal("unknown encoding accepted")
		}
	}
}

// Frozen CryptoJS 4.2.0 output from the old browser implementation:
//
//	encodeURIComponent(CryptoJS.AES.encrypt(password, "nanokvm-sipeed-2024", {
//	  salt: CryptoJS.enc.Hex.parse("0001020304050607")
//	}).toString()). The fixed salt makes these independent compatibility vectors
//
// reproducible; tests must not regenerate them with the server's decoder.
func TestCryptoJS420BrowserVectors(t *testing.T) {
	tests := []struct{ plaintext, raw, escaped string }{
		{"password/+=?& with spaces", "U2FsdGVkX18AAQIDBAUGB7sGW2QcHOMONEief1QAPlEHIuOF4wgNKrnWfuQ7FmJV", "U2FsdGVkX18AAQIDBAUGB7sGW2QcHOMONEief1QAPlEHIuOF4wgNKrnWfuQ7FmJV"},
		{"пароль🔒+ %", "U2FsdGVkX18AAQIDBAUGB9r4f+NqE4e5s2h9NM2wUCDbeyJHBGwx8JC3N4eofTbx", "U2FsdGVkX18AAQIDBAUGB9r4f%2BNqE4e5s2h9NM2wUCDbeyJHBGwx8JC3N4eofTbx"},
	}
	for _, tt := range tests {
		for _, encoding := range []string{"", "cryptojs"} {
			for _, value := range []string{tt.raw, tt.escaped} {
				got, err := DecodeCredentialPassword(value, encoding)
				if err != nil || got != tt.plaintext {
					t.Fatalf("CryptoJS 4.2.0 vector: encoding=%q plaintext matches=%v error=%v", encoding, got == tt.plaintext, err)
				}
			}
		}
	}
}
