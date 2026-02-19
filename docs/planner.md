# Norma Planner (`norma plan`)

The `norma plan` command provides an interactive way to decompose high-level project goals (epics) into a structured Beads hierarchy of epics, features, and tasks.

## Interactive Planning

When you run `norma plan "goal"`, Norma starts an interactive TUI session powered by an LLM agent. The agent will:

1.  **Analyze** your goal.
2.  **Inspect** your current project state using available tools.
3.  **Ask** you clarification questions if the goal is vague or if it needs more context.
4.  **Propose** a decomposition into features and tasks.
5.  **Persist** the final plan to Beads.

## Tools Available to the Planner

The planning agent has access to several tools to help it create accurate and actionable plans.

### `human`
Used by the agent to ask the user a question. The question appears in the TUI, and the agent waits for your response.

### `run_shell_command`
Enables the agent to inspect the codebase and project structure.

*   **Allowed commands:** `ls`, `grep`, `cat`, `find`, `tree`, `git`, `go`, `bd`, `echo`.
*   **Restrictions:**
    *   No pipes (`|`) or redirects (`>`, `>>`).
    *   No command chaining (`&&`, `||`, `;`, `&`).
    *   Commands are executed relative to the repository root.
    *   Timeout is 30 seconds per command.

### `persist_plan`
The final step in the planning process. The agent calls this tool with the complete `Decomposition` object (Epic, Features, and Tasks) once it has finished the planning work.

## Using the Planner

To start a planning session:

```bash
norma plan "Build a REST API for user management with JWT authentication"
```

1.  Follow the prompts in the TUI.
2.  Answer any questions from the agent.
3.  Once the agent has enough information, it will generate the plan.
4.  The final plan will be displayed in the TUI.
5.  Press any key to exit the TUI.
6.  The plan will be persisted to your Beads backlog.
