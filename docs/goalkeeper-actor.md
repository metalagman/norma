# `goalkeeper-actor` playground

Run:

```bash
norma playground goalkeeper-actor "ship the goal" \
  --max-iterations 5 \
  --bridge-bin /path/to/codex-acp-bridge
```

`goalkeeper-actor` is an experimental actor-based runtime variant of Goalkeeper.

It boots:
- one `actorlayer.System`
- one coordinator actor (`goalkeeper-coordinator`)
- one worker ADK actor (`goalkeeper-worker`)
- one validator ADK actor (`goalkeeper-validator`)

The workflow is fixed:
- coordinator sends the goal to worker
- coordinator sends goal + worker output to validator
- validator response must start with `verdict: pass` or `verdict: fail`
- coordinator loops until pass or `--max-iterations` is reached
- coordinator run is one-shot per invocation (single caller request/response)

Worker and validator use:
- ACP-backed ADK agents
- shared ADK in-memory session service
- one conversation id propagated via actor headers

Output contract matches `playground goalkeeper`:
- print final validator output
- print total run time
- exit 0 only when validator verdict is `pass`

This playground does not use:
- Taskmaster queues
- PDCA phase agents
- persistence or remote actor transport
