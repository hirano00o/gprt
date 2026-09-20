package gh

import (
	"testing"

	"github.com/hirano00o/gprt/internal/model"
)

func TestBuildSearchQuery(t *testing.T) {
	tests := []struct {
		name   string
		kind   model.SectionKind
		custom string
		state  string
		want   string
	}{
		{
			"direct review, open",
			model.SectionKindDirectReview, "", "open",
			"type:pr archived:false sort:updated-desc state:open user-review-requested:@me",
		},
		{
			"team review, closed (excludes merged)",
			model.SectionKindTeamReview, "", "closed",
			"type:pr archived:false sort:updated-desc is:closed is:unmerged review-requested:@me -user-review-requested:@me",
		},
		{
			"mine, merged",
			model.SectionKindMine, "", "merged",
			"type:pr archived:false sort:updated-desc is:merged author:@me -review-requested:@me",
		},
		{
			"involved, all (no state term)",
			model.SectionKindInvolved, "", "all",
			"type:pr archived:false sort:updated-desc involves:@me -author:@me -review-requested:@me",
		},
		{
			"custom without its own state gets ours",
			model.SectionKindCustom, "org:acme label:backend", "open",
			"type:pr archived:false sort:updated-desc state:open org:acme label:backend",
		},
		{
			"custom with its own state: is left alone",
			model.SectionKindCustom, "org:acme state:closed", "open",
			"type:pr archived:false sort:updated-desc org:acme state:closed",
		},
		{
			"custom with its own is:merged is left alone",
			model.SectionKindCustom, "org:acme is:merged", "open",
			"type:pr archived:false sort:updated-desc org:acme is:merged",
		},
		{
			"custom with a negated -state: is left alone",
			model.SectionKindCustom, "org:acme -state:closed", "open",
			"type:pr archived:false sort:updated-desc org:acme -state:closed",
		},
		{
			"custom with a negated -is:merged is left alone",
			model.SectionKindCustom, "org:acme -is:merged", "open",
			"type:pr archived:false sort:updated-desc org:acme -is:merged",
		},
		{
			"custom with a negated -is:open is left alone",
			model.SectionKindCustom, "org:acme -is:open", "open",
			"type:pr archived:false sort:updated-desc org:acme -is:open",
		},
		{
			"custom with a negated -is:closed is left alone",
			model.SectionKindCustom, "org:acme -is:closed", "open",
			"type:pr archived:false sort:updated-desc org:acme -is:closed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSearchQuery(tc.kind, tc.custom, tc.state)
			if got != tc.want {
				t.Errorf("BuildSearchQuery(%v, %q, %q) = %q, want %q", tc.kind, tc.custom, tc.state, got, tc.want)
			}
		})
	}
}
