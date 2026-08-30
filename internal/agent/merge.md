Finish this branch and hand its result back to the conversation it was forked from.

Takes no arguments. What crosses back is the proposal document you have been
drafting with `ProposalWrite` and `ProposalEdit`, exactly as it stands. It
becomes the result of the tool call that has been suspended since this branch
started, so write it for the agent that receives it, not for the user: state
what was concluded or produced, and whatever that agent needs in order to carry
on. Merging an empty proposal is an error, not a decision to put to the user.

The user sees the whole proposal as a diff and approves it before it goes
through. If they turn it down, the branch stays open and the proposal is kept —
ask what they want changed, revise it with `ProposalEdit`, and call this tool
again. There is no limit on attempts, and a rejection says nothing about the
quality of the work.

Calling this ends the branch, so call it once the work is actually done.
