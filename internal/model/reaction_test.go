package model

import "testing"

func TestReactionContent_Emoji(t *testing.T) {
	tests := []struct {
		name    string
		content ReactionContent
		want    string
	}{
		{"thumbs up", ReactionThumbsUp, "\U0001F44D"},
		{"thumbs down", ReactionThumbsDown, "\U0001F44E"},
		{"laugh", ReactionLaugh, "\U0001F604"},
		{"hooray", ReactionHooray, "\U0001F389"},
		{"confused", ReactionConfused, "\U0001F615"},
		{"heart", ReactionHeart, "❤️"},
		{"rocket", ReactionRocket, "\U0001F680"},
		{"eyes", ReactionEyes, "\U0001F440"},
		{"unknown", ReactionContent("BOGUS"), "BOGUS"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.content.Emoji(); got != tc.want {
				t.Errorf("Emoji() = %q, want %q", got, tc.want)
			}
		})
	}
}
