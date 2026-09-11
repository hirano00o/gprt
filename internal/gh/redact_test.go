package gh

import "testing"

func TestRedact(t *testing.T) {
	tests := []struct {
		name  string
		token string
		chunk string
		want  string
	}{
		{
			name:  "authorization header line with token scheme",
			token: "",
			chunk: "> Authorization: token ghp_secret123\n",
			want:  "> Authorization: [redacted]\n",
		},
		{
			name:  "authorization header line with bearer scheme, case-insensitive",
			token: "",
			chunk: "authorization: Bearer AbC.123-xyz\n",
			want:  "authorization: [redacted]\n",
		},
		{
			name:  "already-sanitized httpretty block characters are still redacted",
			token: "",
			chunk: "> Authorization: token ████████████████████\n",
			want:  "> Authorization: [redacted]\n",
		},
		{
			name:  "configured token appearing in a body is redacted",
			token: "ghp_secret123",
			chunk: `{"note":"leaked ghp_secret123 here"}`,
			want:  `{"note":"leaked [redacted] here"}`,
		},
		{
			name:  "configured token appears multiple times",
			token: "tok",
			chunk: "tok tok",
			want:  "[redacted] [redacted]",
		},
		{
			name:  "chunk without any secret is unchanged",
			token: "ghp_other",
			chunk: "GET /graphql HTTP/1.1\nAccept: application/json\n",
			want:  "GET /graphql HTTP/1.1\nAccept: application/json\n",
		},
		{
			name:  "empty token argument only applies header redaction",
			token: "",
			chunk: "GET /graphql HTTP/1.1\n> Authorization: token ghp_secret123\n",
			want:  "GET /graphql HTTP/1.1\n> Authorization: [redacted]\n",
		},
		{
			name:  "both a header line and the configured token elsewhere are redacted",
			token: "ghp_secret123",
			chunk: "> Authorization: token ghp_secret123\n{\"note\":\"ghp_secret123 also appears here\"}",
			want:  "> Authorization: [redacted]\n{\"note\":\"[redacted] also appears here\"}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact(tc.token, tc.chunk); got != tc.want {
				t.Errorf("redact(%q, %q) = %q, want %q", tc.token, tc.chunk, got, tc.want)
			}
		})
	}
}
