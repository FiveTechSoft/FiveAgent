package sandbox

import "testing"

// Battery run 6 (2026-10-02): WSL's bash.exe writes UTF-16LE, so the
// model received "A\x00c\x00c\x00e\x00s\x00o..." instead of the error
// and invented one. Decoding is only for streams that really are
// UTF-16; ordinary output must come back byte-identical.
func TestDecodeUTF16(t *testing.T) {
	utf16LE := func(s string) string {
		var b []byte
		for _, r := range s {
			b = append(b, byte(r), byte(r>>8))
		}
		return string(b)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii untouched", "no output, exit code 1", "no output, exit code 1"},
		{"utf-8 accents untouched", "Código de error: no encontrado", "Código de error: no encontrado"},
		{"short input untouched", "a\x00", "a\x00"},
		{"utf-16le decoded", utf16LE("Acceso denegado."), "Acceso denegado."},
		{"utf-16le with bom", "\xff\xfe" + utf16LE("Bash/E_ACCESSDENIED"), "Bash/E_ACCESSDENIED"},
	}
	for _, c := range cases {
		if got := decodeUTF16(c.in); got != c.want {
			t.Errorf("%s: decodeUTF16(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
