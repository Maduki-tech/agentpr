# agentpr

A review loop that works like a pull request (but isn't one) between a human Reviewer and a coding agent: the agent changes code, the Reviewer comments on the diff, the agent answers and fixes.

## Language

**Author**:
The coding agent (currently Claude Code) whose changes are under review.
_Avoid_: AI, bot, worker

**Reviewer**:
The human reading the diff and leaving comments.
_Avoid_: User

**Base**:
The commit the Author's work is compared against: a branch's merge-base with the main branch (or with whichever branch the Reviewer picks), or, on the main branch itself, its upstream (`HEAD` without one).

**Changeset**:
The Author's current changes on the branch, compared to the Base. Reviewed like a pull request, but it is not one: it has no lifecycle of its own and is simply whatever has changed right now.
_Avoid_: Pull Request, PR, diff

**Review**:
One round of Reviewer feedback on a Changeset, ending with a Verdict.

**Verdict**:
The outcome of a Review: _Request changes_ (the Author must act on open Threads) or _Approve_ (the Changeset is accepted).

**Thread**:
A conversation anchored to a line range, a file, or the whole Changeset, made of Comments from the Reviewer and the Author, and resolved only by the Reviewer.
_Avoid_: Note, annotation

**Comment**:
A single message inside a Thread.

**Pending Comment**:
A Comment the Reviewer has written but not yet sent; it becomes visible to the Author only when the Review is submitted.
_Avoid_: Draft

**Outdated Thread**:
A Thread whose original snippet can no longer be found in the Changeset; it stays visible with the snippet it was written on.

**Pull request (PR)**:
A real GitHub pull request, opened from the branch with the Action button once the Changeset is ready to ship. Not the Changeset itself.

**Action button**:
The button at the right of the toolbar that ships the Changeset: its label and action follow the branch's state (commit, push, create PR, merge PR), and its menu offers each step on its own.
