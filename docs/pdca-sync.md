# `pdca-sync` playground

Run:

```bash
norma playground pdca-sync "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge \
  --max-iterations 5
```

`pdca-sync` is an experimental **synchronous** PDCA playground.

In the current playground family:
- `taskmaster` = generic async harness
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA wrapper

It boots:
- one root coordinator agent
- four fixed child agents: `plan`, `do`, `check`, `act`

This playground uses:
- ACP-backed ADK agents
- plain-text prompts only
- plain-text child outputs only
- one internal synchronous MCP control tool for child prompting
- a model-backed coordinator root agent

This playground does **not** use:
- Taskmaster tasks
- envelopes
- `report_to`
- async queues
- completion notifications
- structured PDCA JSON contracts

## Runtime model

The coordinator root agent gets one goal prompt and may synchronously prompt any child agent through the internal MCP tool:

- `pdca.prompt_subagent`

The tool accepts:
- `agent_name`
- `prompt`

and returns only the child agent's plain-text result.

Child invocation is blackbox:
- the runtime chooses and runs the target child agent
- the child sees only the plain prompt string
- the coordinator sees only the plain-text result

## Iterations

`pdca-sync` does not enforce phase order.

The only loop guard is `--max-iterations`:
- iteration `1` starts when the run starts
- invoking `plan` after an `act` call starts the next iteration
- if that would exceed `max_iterations`, the tool returns an error instead of invoking `plan`

## Prompt contract

The child prompts match the current Taskmaster child prompts:
- `plan` plans
- `do` executes
- `check` emits the semantic PDCA verdict `pass` or `fail`
- `act` emits the semantic PDCA decision `close`, `continue`, or `replan`

All child messaging remains plain text.

The PDCA semantic layer is still canonical:
- check verdict literals: `pass`, `fail`
- act decision literals: `close`, `continue`, `replan`
- legal pairs:
  - `pass + close`
  - `fail + continue`
  - `fail + replan`
- invalid pairs:
  - `pass + continue`
  - `pass + replan`
  - `fail + close`
  - any `rollback`

Semantic meaning:
- `close` means the work is done
- `continue` means run another PDCA iteration
- `replan` means the work is not ready to close and needs replanning

The coordinator root:
- must not invent worker methodology
- may choose child invocation order freely
- ends the run by returning its own final plain-text answer
