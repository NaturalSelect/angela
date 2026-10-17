Stops a background agent you launched from this conversation with the Agent tool's `run_in_background` parameter. Use it when the user no longer needs the work, or when what the agent was doing has been overtaken by something else.

The agent ends shortly after the call. You are then notified with the status "stopped", the same way as for any other ending, and whatever the agent had produced up to that point is not returned. Stopping an agent that already finished does nothing.

Only stop an agent for one of those two reasons. Do not stop an agent because it is taking a while: a running agent will notify you when it is done.
