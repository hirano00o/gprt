package theme

import "testing"

func TestUnicodeAndNerdIconsDiffer(t *testing.T) {
	u, n := Unicode(), Nerd()
	if u == n {
		t.Fatal("Unicode() and Nerd() returned identical icon sets")
	}
	if u.Draft == "" || n.Draft == "" {
		t.Error("Draft icon must not be empty in either set")
	}
	if u.SectionMarker == "" || n.SectionMarker == "" {
		t.Error("SectionMarker icon must not be empty in either set")
	}
}

func TestIconsFor(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   Icons
	}{
		{"unicode explicit", "unicode", Unicode()},
		{"nerd explicit", "nerd", Nerd()},
		{"empty defaults to unicode", "", Unicode()},
		{"unrecognized defaults to unicode", "bogus", Unicode()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IconsFor(tc.config); got != tc.want {
				t.Errorf("IconsFor(%q) = %+v, want %+v", tc.config, got, tc.want)
			}
		})
	}
}
