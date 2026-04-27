# Swarm Harness

The swarm harness is a second orchestration mode alongside PDCA.

Command surface:

```bash
norma swarm <epic-id>
```

Swarm runs configured role agents against Beads tasks routed by `assignee`. Unlike PDCA, swarm tasks are plain freeform task text. The harness does not require Plan/Do/Check/Act JSON contracts and does not interpret agent prose as the source of truth. It follows Beads task state after each agent session.

## Swarm vs PDCA

Swarm and PDCA solve different problems:

| Workflow | Task contract | Completion source of truth | Typical use |
| --- | --- | --- | --- |
| `norma loop` | Structured PDCA role JSON | PDCA verdict + act decision | One task worked through a strict Plan -> Do -> Check -> Act loop |
| `norma swarm` | Freeform task text | Beads task state and assignee changes | Multi-role task routing across a Beads epic |

PDCA is execution-contract driven.
Swarm is assignment- and state-driven.

## Config

Swarm configuration lives under the top-level `swarm` key in `.norma/config.yaml`:

```yaml
swarm:
  primary_role: coordinator
  default_provider: codex_acp_agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: Decide routing, resolve bounced tasks, supervise swarm progress.
    planner:
      assignee: norma-planner
      instruction: Break down work and assign tasks to roles.
      provider: gemini_acp_agent
    implementer:
      assignee: norma-implementer
      instruction: Implement assigned tasks.
      provider: opencode_acp_agent

profiles:
  opencode:
    swarm:
      default_provider: opencode_acp_agent
```

Rules:

- `primary_role` is required and must point to one entry in `roles`.
- `default_provider` is required and must exist in `runtime.providers`.
- `roles.<key>.provider` is optional. If omitted, Norma uses `default_provider`.
- `roles.<key>.assignee` is required and must be unique.
- `roles.<key>.instruction` is required and becomes that role agent's instruction.

## Assignment Model

Initial assignment is human-owned.

That means:

- A ready task with no `assignee` is not auto-routed.
- The swarm reports that task and skips it.
- The first runnable task for a role must already be assigned in Beads.

After bootstrap, role agents may coordinate by mutating Beads through `norma.tasks.*` MCP tools:

- `norma.tasks.set_assignee`
- `norma.tasks.add_follow_up`
- `norma.tasks.add_dependency`
- `norma.tasks.update`
- `norma.tasks.close_with_reason`

Agents do not communicate directly. Coordination happens through Beads state changes only.

## Routing Semantics

Swarm starts from the provided epic, but the live queue is assignment-defined after startup.

Runtime rules:

- Ready tasks assigned to a configured swarm assignee are eligible for execution.
- Tasks under the starting epic are the bootstrap scope.
- Off-epic tasks are admitted once they are assigned to a configured swarm role.
- Each configured role processes at most one task at a time.
- Each task execution uses a fresh ADK session.

## Outcome Inference

After a role agent session finishes, Norma reloads the task from Beads and infers the result from task state:

| Beads state after session | Meaning | Norma behavior |
| --- | --- | --- |
| Task closed / `done` | Completed | Accept completion |
| Task still open, assignee changed | Handed off | Leave open and let the new assignee pick it up |
| Task still open, same assignee | No progress | Reassign to the primary coordinator |
| Task still open, assignee empty | Human triage required | Report and skip |

The harness intentionally trusts Beads state, not the agent's final text response.

## Prompt Shape

Swarm prompts stay small and plain:

- task id
- epic id
- working directory / `cwd`
- artifact directory
- task title
- task description / goal

Task text stays freeform. Swarm does not wrap the task in PDCA role schemas.

## Example

Given:

- epic `norma-phmp`
- task `norma-phmp.3.1`
- assignee `norma-planner`

Running:

```bash
norma swarm norma-phmp
```

causes Norma to:

1. load `runtime` + `swarm`
2. build one role agent instance per configured role
3. start an embedded task MCP server
4. scan ready leaf tasks
5. dispatch `norma-phmp.3.1` to the role whose `assignee` is `norma-planner`
6. reload the task from Beads after the session
7. infer completion, handoff, coordinator bounce, or human-triage from Beads state
