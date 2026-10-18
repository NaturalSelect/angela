Shows the background agents you launched from this conversation with the Agent tool's `run_in_background` parameter: whether each is still running, or has completed, failed or been stopped. Pass `agent_id` to look at one agent, or leave it out to list them all.

Never use this to wait. While an agent runs it only reports "running", and the agent's output is not available until it finishes. When it finishes, the result reaches you in a notification without a call: between your tool calls if you are still working, or by starting a new turn if you have already replied. If that result is all you are waiting for, end your turn.

Use it to check once on agents you have lost track of, or to answer a user who asks what is still running. The result carries no agent output; a completed agent's report arrives in its notification and can be loaded again with `LoadReport`.
