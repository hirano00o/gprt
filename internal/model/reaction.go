package model

// ReactionContent identifies one of the eight reactions GitHub allows on a
// pull request, issue comment, review comment, or review.
type ReactionContent string

// The eight reaction contents supported by the GitHub API.
const (
	ReactionThumbsUp   ReactionContent = "THUMBS_UP"
	ReactionThumbsDown ReactionContent = "THUMBS_DOWN"
	ReactionLaugh      ReactionContent = "LAUGH"
	ReactionHooray     ReactionContent = "HOORAY"
	ReactionConfused   ReactionContent = "CONFUSED"
	ReactionHeart      ReactionContent = "HEART"
	ReactionRocket     ReactionContent = "ROCKET"
	ReactionEyes       ReactionContent = "EYES"
)

// emojiByReaction maps each known reaction content to the emoji glyph the UI
// displays for it.
var emojiByReaction = map[ReactionContent]string{
	ReactionThumbsUp:   "\U0001F44D",
	ReactionThumbsDown: "\U0001F44E",
	ReactionLaugh:      "\U0001F604",
	ReactionHooray:     "\U0001F389",
	ReactionConfused:   "\U0001F615",
	ReactionHeart:      "❤️",
	ReactionRocket:     "\U0001F680",
	ReactionEyes:       "\U0001F440",
}

// Emoji returns the display glyph for the reaction content. Unknown values
// (which should not occur against a well-behaved API) are returned as-is so
// callers always have something displayable.
func (c ReactionContent) Emoji() string {
	if emoji, ok := emojiByReaction[c]; ok {
		return emoji
	}
	return string(c)
}

// ReactionGroup aggregates all reactions of one content on a reactable
// subject (pull request, issue comment, review comment, or review).
type ReactionGroup struct {
	Content          ReactionContent
	Count            int
	ViewerHasReacted bool
}
