package domain

// DisabledReason is why the product, rather than a person, switched a watcher or
// a scheduled task off (T-Z8).
//
// Roadmap 12's decision 11: an unattended job runs as the person who created it
// and is re-checked when it fires, and *"a revoked grant disables the task and
// says why. A cron that keeps working after its author lost access is a hole with
// a schedule attached."* A job that stopped with no reason on it reads exactly
// like one that broke, so the reason is stored beside `enabled` and cleared the
// moment somebody switches it back on.
//
// Both reasons are about a **restricted** agent. A job on an open agent is not
// re-checked at all — open means every member, and decision 3 promises nothing
// changes for a workspace with nothing restricted — so a job whose creator left
// keeps running on an open agent exactly as it did before this ticket.
type DisabledReason string

const (
	// DisabledReasonCreatorNotGranted — the agent it runs as is restricted, and the
	// person who created it is not granted it.
	DisabledReasonCreatorNotGranted DisabledReason = "creator_not_granted"
	// DisabledReasonCreatorRemoved — the agent it runs as is restricted, and the
	// person who created it is no longer in the workspace: removed by an admin,
	// or deleted outright. Checked apart from the grant, because removing a
	// member deactivates the row rather than deleting it, and their grant rows
	// survive a deactivation.
	DisabledReasonCreatorRemoved DisabledReason = "creator_removed"
)

// Sentence is what a scheduled task's failed run says, so its history explains
// the stop in words rather than as a code. A watcher's event carries the code,
// and the dashboard has its own shorter copy beside the badge.
func (r DisabledReason) Sentence() string {
	switch r {
	case DisabledReasonCreatorNotGranted:
		return "Turned off: the agent this runs as is restricted, and the person who created it is not granted it. An admin can grant them and switch it back on."
	case DisabledReasonCreatorRemoved:
		return "Turned off: the agent this runs as is restricted, and the person who created it is no longer in this workspace. Recreate it as someone who is granted the agent."
	default:
		return "Turned off by an access check."
	}
}
