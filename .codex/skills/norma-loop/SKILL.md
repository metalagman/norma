---
name: norma-loop
description: Guide for running and improving the Norma PDCA development lifecycle (plan/do/check/act), including required artifacts, budgets, workspace isolation, and Beads-only task state. Use when working on Norma loop behavior, run orchestration, or agent step expectations.
---

# norma-loop

Follow the Norma PDCA lifecycle with strict artifacts, measurable objectives, and isolated workspaces. This skill is a concise operational guide. For the authoritative, full spec, read `AGENTS.md` at the repo root before making changes.

## Core invariants

- Use the single fixed workflow: plan -> do -> check -> act (repeat until PASS or budgets exceeded).
- Task state and backlog live only in Beads (`bd`); do not mirror task status in Norma DB.
- Run/step state lives in SQLite; artifacts are files on disk.
- Each run uses an isolated Git worktree in `runs/<run_id>/workspace/` on branch `norma/task/<task_id>`.
- Shared artifacts live in `runs/<run_id>/artifacts/`; step-local data stays in `runs/<run_id>/steps/<n>-<role>/`.
- Agents write only to their `step_dir` and the shared `artifacts_dir`, except Do/Act which modify the workspace.
- Git history/diff is the source of truth for changes to apply back to the main repo.

## PDCA responsibilities (development lifecycle)

### Plan

- Define the problem, gather current-state data, and analyze root causes.
- Set specific, measurable objectives and success metrics for the iteration.
- Produce a detailed plan with timeline expectations, resource needs, stakeholders, and risks.
- Output an ordered backlog, Next Slice (1 feature + 1-3 tasks), stop conditions, and a verification checklist.
- Ensure Ready Contract fields exist for the selected task: Objective, Artifact, Verify.

### Do

- Implement exactly one Ready leaf task as a controlled pilot; do not expand scope.
- Document actions, observations, and unexpected issues (e.g., in `artifacts/evidence/`).
- If scope grows or uncertainty appears, split into new tasks or spikes and stop.

### Check

- Evaluate results against Plan metrics and Verify commands.
- Record evidence files, summarize pass/fail per criterion, and analyze deviations.
- Identify what worked, what did not, and likely root causes.

### Act

- If PASS: standardize the change (procedures/docs), close the task, and update progress notes.
- If FAIL/PARTIAL: adjust approach, create fix tasks/spikes, update dependencies, and prepare the next plan.
- Communicate outcomes and next steps to stakeholders via `progress.md` updates.

## Role outputs and required artifacts

- Plan: write `artifacts/plan.md` and AgentResponse `output.json`.
- Do: modify workspace and write evidence files under `artifacts/`.
- Check: write `artifacts/verdict.json` and `artifacts/scorecard.md` plus evidence.
- Act: propose and, if required, implement fixes in workspace; summarize targeted ACs.

## Budgets and stop conditions

- Stop immediately when any budget is exceeded (e.g., max_iterations).
- Trigger re-plan if task size grows, requirements are missing, dependencies block, or retries exceed limits.

## Workflow hygiene

- Keep each iteration bounded to a single task or small slice.
- Make verification explicit and repeatable; Check must reference evidence files.
- Treat `artifacts/` as the shared memory for the run; keep it human-readable.

