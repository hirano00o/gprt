package model

// CheckStatus is the lifecycle status of a check run or commit status, as
// reported by GitHub's status-check rollup.
type CheckStatus string

// Known check statuses. Every value other than CheckStatusCompleted is
// treated as pending by Summarize.
const (
	CheckStatusQueued     CheckStatus = "QUEUED"
	CheckStatusInProgress CheckStatus = "IN_PROGRESS"
	CheckStatusCompleted  CheckStatus = "COMPLETED"
	CheckStatusPending    CheckStatus = "PENDING"
	CheckStatusWaiting    CheckStatus = "WAITING"
	CheckStatusRequested  CheckStatus = "REQUESTED"
	// CheckStatusExpected is the legacy commit-status API's "expected"
	// state (its equivalent of "pending").
	CheckStatusExpected CheckStatus = "EXPECTED"
)

// CheckConclusion is the outcome of a completed check run or commit status.
// It is meaningful only when the check's Status is CheckStatusCompleted.
type CheckConclusion string

// Known check conclusions, matching GitHub's CheckConclusionState plus
// CheckConclusionError for the legacy commit-status API's "error" state
// (which has no counterpart in CheckConclusionState).
const (
	CheckConclusionSuccess        CheckConclusion = "SUCCESS"
	CheckConclusionFailure        CheckConclusion = "FAILURE"
	CheckConclusionSkipped        CheckConclusion = "SKIPPED"
	CheckConclusionNeutral        CheckConclusion = "NEUTRAL"
	CheckConclusionError          CheckConclusion = "ERROR"
	CheckConclusionCancelled      CheckConclusion = "CANCELLED"
	CheckConclusionTimedOut       CheckConclusion = "TIMED_OUT"
	CheckConclusionActionRequired CheckConclusion = "ACTION_REQUIRED"
	CheckConclusionStartupFailure CheckConclusion = "STARTUP_FAILURE"
	CheckConclusionStale          CheckConclusion = "STALE"
)

// Check is a single check run or commit status attached to a pull request's
// head commit.
type Check struct {
	Name       string
	Status     CheckStatus
	Conclusion CheckConclusion
	URL        string
	Workflow   string
	IsRequired bool
}

// ChecksSummary tallies a pull request's checks by outcome.
type ChecksSummary struct {
	Total   int
	Success int
	Failure int
	Pending int
	Skipped int
}

// Summarize tallies checks into a ChecksSummary, classifying each check the
// way "gh pr checks" does. A check counts as Pending unless it is
// CheckStatusCompleted, in which case it is classified by its Conclusion:
// CheckConclusionSuccess is a success; CheckConclusionSkipped and
// CheckConclusionNeutral are skipped; every other conclusion (including an
// empty or unrecognised one) counts as a failure. A completed check is
// never dropped from the tally: an unknown outcome must not be reported as
// green.
func Summarize(checks []Check) ChecksSummary {
	var summary ChecksSummary
	for _, c := range checks {
		summary.Total++
		if c.Status != CheckStatusCompleted {
			summary.Pending++
			continue
		}
		switch c.Conclusion {
		case CheckConclusionSuccess:
			summary.Success++
		case CheckConclusionSkipped, CheckConclusionNeutral:
			summary.Skipped++
		default:
			summary.Failure++
		}
	}
	return summary
}

// ChecksState is the overall rollup state derived from a ChecksSummary,
// used to pick the icon shown next to a pull request in the list.
type ChecksState string

// Possible overall check states.
const (
	ChecksStateNone    ChecksState = "none"
	ChecksStateSuccess ChecksState = "success"
	ChecksStateFailure ChecksState = "failure"
	ChecksStatePending ChecksState = "pending"
)

// State derives the overall rollup state: no checks report as
// ChecksStateNone; any failure wins over pending; otherwise any pending
// check makes the whole rollup pending; success (with any number of
// skipped) otherwise.
func (s ChecksSummary) State() ChecksState {
	switch {
	case s.Total == 0:
		return ChecksStateNone
	case s.Failure > 0:
		return ChecksStateFailure
	case s.Pending > 0:
		return ChecksStatePending
	default:
		return ChecksStateSuccess
	}
}
