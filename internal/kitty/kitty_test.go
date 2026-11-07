package kitty

import (
	"strings"
	"testing"
)

func TestDiacriticsTable(t *testing.T) {
	if len(diacritics) != 297 || MaxCells != 297 {
		t.Fatalf("len(diacritics) = %d, MaxCells = %d, want 297", len(diacritics), MaxCells)
	}
	head := []rune{0x0305, 0x030D, 0x030E, 0x0310}
	for i, want := range head {
		if diacritics[i] != want {
			t.Errorf("diacritics[%d] = %U, want %U", i, diacritics[i], want)
		}
	}
	tail := []rune{0x1D242, 0x1D243, 0x1D244}
	for i, want := range tail {
		if got := diacritics[len(diacritics)-len(tail)+i]; got != want {
			t.Errorf("diacritics[%d] = %U, want %U", len(diacritics)-len(tail)+i, got, want)
		}
	}
	seen := map[rune]bool{}
	for i, d := range diacritics {
		if seen[d] || (i > 0 && d <= diacritics[i-1]) {
			t.Fatalf("diacritics[%d] = %U is duplicated or out of order", i, d)
		}
		seen[d] = true
	}
}

func TestCell(t *testing.T) {
	got := Cell(1, 2)
	want := "\U0010EEEE" + string(diacritics[1]) + string(diacritics[2])
	if got != want {
		t.Errorf("Cell(1, 2) = %q, want %q", got, want)
	}
	// Out-of-range numbers clamp to the table's last entry instead of panicking.
	if got, want := Cell(1000, -3), "\U0010EEEE"+string(diacritics[MaxCells-1])+string(diacritics[0]); got != want {
		t.Errorf("Cell(1000, -3) = %q, want %q", got, want)
	}
}

func TestRows(t *testing.T) {
	rows := Rows(3, 2)
	if len(rows) != 2 {
		t.Fatalf("len(Rows(3, 2)) = %d, want 2", len(rows))
	}
	for r, line := range rows {
		want := Cell(r, 0) + Cell(r, 1) + Cell(r, 2)
		if line != want {
			t.Errorf("Rows(3, 2)[%d] = %q, want %q", r, line, want)
		}
	}
	if got := Rows(MaxCells+50, 1)[0]; strings.Count(got, "\U0010EEEE") != MaxCells {
		t.Errorf("Rows(too wide) has %d cells, want it capped at %d", strings.Count(got, "\U0010EEEE"), MaxCells)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{"TERM": "xterm-256color"}, false},
		{"kitty window id", map[string]string{"KITTY_WINDOW_ID": "3", "TERM": "tmux-256color"}, true},
		{"ghostty resources dir", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x", "TERM": "screen"}, true},
		{"term xterm-kitty", map[string]string{"TERM": "xterm-kitty"}, true},
		{"term xterm-ghostty", map[string]string{"TERM": "xterm-ghostty"}, true},
		{"term program ghostty", map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm"}, true},
		{"other term program", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false},
		{"empty kitty window id", map[string]string{"KITTY_WINDOW_ID": ""}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("Detect() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTransmitSequence_SingleChunk(t *testing.T) {
	got := transmitSequence(7, 3, 2, []byte("hello"), false)
	want := "\x1b_Ga=T,U=1,f=100,t=d,i=7,c=3,r=2,q=2,m=0;aGVsbG8=\x1b\\"
	if got != want {
		t.Errorf("transmitSequence() = %q, want %q", got, want)
	}
}

func TestTransmitSequence_Chunking(t *testing.T) {
	// 6144 bytes encode to exactly 8192 base64 characters: two full chunks.
	payload := make([]byte, 6144+3)
	for i := range payload {
		payload[i] = byte(i)
	}
	got := transmitSequence(9, 10, 4, payload, false)

	parts := strings.Split(strings.TrimSuffix(got, "\x1b\\"), "\x1b\\")
	if len(parts) != 3 {
		t.Fatalf("got %d chunks, want 3: %q", len(parts), got)
	}
	headers := []string{
		"\x1b_Ga=T,U=1,f=100,t=d,i=9,c=10,r=4,q=2,m=1;",
		"\x1b_Gm=1;",
		"\x1b_Gm=0;",
	}
	total := 0
	for i, p := range parts {
		if !strings.HasPrefix(p, headers[i]) {
			t.Errorf("chunk %d = %.60q, want prefix %q", i, p, headers[i])
		}
		data := strings.TrimPrefix(p, headers[i])
		if len(data) > 4096 {
			t.Errorf("chunk %d payload is %d bytes, want at most 4096", i, len(data))
		}
		total += len(data)
	}
	if want := (len(payload) + 2) / 3 * 4; total != want {
		t.Errorf("total base64 length = %d, want %d", total, want)
	}
}

func TestDeleteSequence(t *testing.T) {
	if got, want := deleteSequence(12, false), "\x1b_Ga=d,d=I,i=12,q=2\x1b\\"; got != want {
		t.Errorf("deleteSequence() = %q, want %q", got, want)
	}
}

func TestTmuxWrapping(t *testing.T) {
	plain := "\x1b_Ga=d,d=I,i=1,q=2\x1b\\"
	want := "\x1bPtmux;\x1b\x1b_Ga=d,d=I,i=1,q=2\x1b\x1b\\\x1b\\"
	if got := deleteSequence(1, true); got != want {
		t.Errorf("deleteSequence(tmux) = %q, want %q", got, want)
	}
	if got := wrapTmux(plain); got != want {
		t.Errorf("wrapTmux() = %q, want %q", got, want)
	}

	// Every chunk of a multi-chunk transmission is wrapped on its own.
	got := transmitSequence(1, 1, 1, make([]byte, 5000), true)
	if n := strings.Count(got, "\x1bPtmux;"); n != 2 {
		t.Errorf("tmux wrappers = %d, want one per chunk (2)", n)
	}
	if n := strings.Count(got, "\x1b\x1b_G"); n != 2 {
		t.Errorf("ESC-doubled APC introducers = %d, want 2", n)
	}
}

func TestInTmux(t *testing.T) {
	if InTmux(func(string) string { return "" }) {
		t.Error("InTmux() = true without $TMUX")
	}
	if !InTmux(func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux-501/default,1,0"
		}
		return ""
	}) {
		t.Error("InTmux() = false with $TMUX set")
	}
}
