package inlineimg

import "testing"

const (
	testUUID  = "0b9a8c3e-1d2f-4a5b-9c6d-7e8f9a0b1c2d"
	otherUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func TestResolver_URL(t *testing.T) {
	signed := "https://private-user-images.githubusercontent.com/1/2-" + testUUID + ".png?jwt=abc&amp;x=y"
	signedWant := "https://private-user-images.githubusercontent.com/1/2-" + testUUID + ".png?jwt=abc&x=y"
	attach := "https://github.com/user-attachments/assets/" + testUUID

	tests := []struct {
		name     string
		bodyHTML string
		src      string
		want     string
		wantOK   bool
	}{
		{
			name:     "upload matched by shared asset uuid, entities unescaped",
			bodyHTML: `<p><img width="800" alt="x" src="` + signed + `"></p>`,
			src:      attach,
			want:     signedWant,
			wantOK:   true,
		},
		{
			name: "external image matched by data-canonical-src",
			bodyHTML: `<a href="https://example.com/a.png"><img src="https://camo.githubusercontent.com/abc" ` +
				`data-canonical-src="https://example.com/a.png" alt="a"></a>`,
			src:    "https://example.com/a.png",
			want:   "https://camo.githubusercontent.com/abc",
			wantOK: true,
		},
		{
			name:     "canonical src with escaped ampersand",
			bodyHTML: `<img src="https://camo.githubusercontent.com/abc" data-canonical-src="https://example.com/a.png?x=1&amp;y=2">`,
			src:      "https://example.com/a.png?x=1&y=2",
			want:     "https://camo.githubusercontent.com/abc",
			wantOK:   true,
		},
		{
			name:     "exact src equality",
			bodyHTML: `<img src='https://example.com/b.png'>`,
			src:      "https://example.com/b.png",
			want:     "https://example.com/b.png",
			wantOK:   true,
		},
		{
			name:     "different uuid does not match",
			bodyHTML: `<img src="` + signed + `">`,
			src:      "https://github.com/user-attachments/assets/" + otherUUID,
			wantOK:   false,
		},
		{
			name:     "uuid only counts for user-attachments sources",
			bodyHTML: `<img src="` + signed + `">`,
			src:      "https://example.com/" + testUUID + ".png",
			wantOK:   false,
		},
		{
			name:     "no images in the html",
			bodyHTML: `<p>hello</p>`,
			src:      attach,
			wantOK:   false,
		},
		{
			name:     "empty html",
			bodyHTML: ``,
			src:      attach,
			wantOK:   false,
		},
		{
			name:     "second image of several",
			bodyHTML: `<img src="https://camo.githubusercontent.com/1" data-canonical-src="https://e.com/1.png"><img src="https://camo.githubusercontent.com/2" data-canonical-src="https://e.com/2.png">`,
			src:      "https://e.com/2.png",
			want:     "https://camo.githubusercontent.com/2",
			wantOK:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Resolve(tc.bodyHTML).URL(tc.src)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("URL(%q) = %q, %v; want %q, %v", tc.src, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
