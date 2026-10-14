# The Changeset is a plain diff against the Base, without per-author attribution

The Changeset is everything that differs from the Base (committed, uncommitted, and untracked-but-not-ignored), recomputed on every start, with no frozen Base. So the Reviewer's own edits show up alongside the Author's. We rejected per-turn snapshots via Claude Code hooks: they would separate the two, but they add hook installation and snapshot storage for a case that rarely happens (the Reviewer seldom edits while the Author works). Revisit if mixed edits become a real problem.
