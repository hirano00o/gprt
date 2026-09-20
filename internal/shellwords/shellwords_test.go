package shellwords

import "testing"

func TestSplit(t *testing.T) {
	t.Setenv("HOME", "/home/u")

	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{
			name: "a double-quoted argument containing a space stays one field",
			in:   `open -a "Google Chrome"`,
			want: []string{"open", "-a", "Google Chrome"},
		},
		{
			name: "an empty single-quoted field is kept, not dropped",
			in:   `emacsclient -c -a ''`,
			want: []string{"emacsclient", "-c", "-a", ""},
		},
		{
			name: "a backslash-escaped space stays within one field",
			in:   `a\ b c`,
			want: []string{"a b", "c"},
		},
		{
			name: "adjacent quoted parts merge into a single field",
			in:   `x 'it''s'`,
			want: []string{"x", "its"},
		},
		{
			name: "$VAR expands against the environment",
			in:   `$HOME/y`,
			want: []string{"/home/u/y"},
		},
		{
			name: "${VAR} expands against the environment",
			in:   `${HOME}z`,
			want: []string{"/home/uz"},
		},
		{
			name: "~ expands to the home directory",
			in:   `~/y`,
			want: []string{"/home/u/y"},
		},
		{
			name: "a glob pattern is returned literally, not expanded",
			in:   `a*b`,
			want: []string{"a*b"},
		},
		{
			name: "an empty string produces no fields and no error",
			in:   ``,
			want: nil,
		},
		{
			name:    "an unterminated quote is a parse error",
			in:      `x "unterminated`,
			wantErr: true,
		},
		{
			name:    "command substitution is rejected",
			in:      `x $(echo hi)`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Split(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Split(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("Split(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
