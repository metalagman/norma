package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
)

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage norma tasks",
	}
	cmd.AddCommand(taskAddCmd())
	cmd.AddCommand(taskListCmd())
	cmd.AddCommand(taskDoneCmd())
	cmd.AddCommand(taskLinkCmd())
	return cmd
}

func taskAddCmd() *cobra.Command {
	var runID string
	var acList []string

	cmd := &cobra.Command{
		Use:   "add <goal>",
		Short: "Add a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			goal := strings.TrimSpace(strings.Join(args, " "))
			if goal == "" {
				return fmt.Errorf("goal is required")
			}
			tracker := task.NewBeadsTracker("")
			ctx := cmd.Context()
			trimmedRunID := strings.TrimSpace(runID)
			var runIDPtr *string
			if trimmedRunID != "" {
				r := trimmedRunID
				runIDPtr = &r
			}
			ac := normalizeAC(acList)
			id, err := tracker.Add(ctx, goal, goal, ac, runIDPtr)
			if err != nil {
				return err
			}
			fmt.Printf("task %s added\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "associate task with a run")
	cmd.Flags().StringArrayVar(&acList, "ac", nil, "acceptance criterion text (repeatable)")
	return cmd
}

func taskListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			tracker := task.NewBeadsTracker("")
			var statusPtr *string
			if status != "" {
				statusPtr = &status
			} else {
				statusPtr = nil
			}
			items, err := tracker.List(context.Background(), statusPtr)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Println("no tasks")
				return nil
			}
			for _, item := range items {
				run := "-"
				if item.RunID != nil {
					run = *item.RunID
				}
				title := item.Title
				if title == "" {
					title = item.Goal
				}
				fmt.Printf("%s\t%s\t%s\t%s\n", item.ID, item.Status, run, title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (todo|doing|done|failed|stopped)")
	return cmd
}

func taskDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			if err := tracker.MarkDone(context.Background(), id); err != nil {
				return err
			}
			fmt.Printf("task %s done\n", id)
			return nil
		},
	}
	return cmd
}

func taskLinkCmd() *cobra.Command {
	var dependsOn []string
	cmd := &cobra.Command{
		Use:   "link <task-id>",
		Short: "Link a task to dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			if len(dependsOn) == 0 {
				return fmt.Errorf("at least one --depends-on id is required")
			}
			tracker := task.NewBeadsTracker("")
			for _, dep := range dependsOn {
				if dep == taskID {
					return fmt.Errorf("task cannot depend on itself")
				}
				if err := tracker.AddDependency(context.Background(), taskID, dep); err != nil {
					return err
				}
			}
			fmt.Printf("task %s linked\n", taskID)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&dependsOn, "depends-on", nil, "task id this task depends on (repeatable)")
	return cmd
}

func runTaskByID(ctx context.Context, tracker task.Tracker, runStore *run.Store, runner *run.Runner, id string) error {
	item, err := tracker.Get(ctx, id)
	if err != nil {
		return err
	}
	switch item.Status {
	case "todo", "failed", "stopped":
	case "doing":
		if item.RunID != nil {
			status, err := runStore.GetRunStatus(ctx, *item.RunID)
			if err != nil {
				return err
			}
			if status == "running" {
				return fmt.Errorf("task %s already running", id)
			}
		}
		if err := tracker.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("task %s status is %s", id, item.Status)
	}
	if err := tracker.MarkStatus(ctx, id, "doing"); err != nil {
		return err
	}
	result, err := runner.Run(ctx, item.Goal, item.Criteria)
	if err != nil {
		_ = tracker.MarkStatus(ctx, id, "failed")
		return err
	}
	if result.RunID != "" {
		_ = tracker.SetRun(ctx, id, result.RunID)
	}
	switch result.Status {
	case "passed":
		if err := tracker.MarkStatus(ctx, id, "done"); err != nil {
			return err
		}
		fmt.Printf("task %s passed (run %s)\n", id, result.RunID)
		return nil
	case "failed":
		if err := tracker.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	case "stopped":
		if err := tracker.MarkStatus(ctx, id, "stopped"); err != nil {
			return err
		}
		return fmt.Errorf("task %s stopped (run %s)", id, result.RunID)
	default:
		if err := tracker.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	}
}

func runLeafTasks(ctx context.Context, tracker task.Tracker, runStore *run.Store, runner *run.Runner, continueOnFail bool, policy task.SelectionPolicy) error {
	for {
		readyTasks, err := tracker.LeafTasks(ctx)
		if err != nil {
			return err
		}
		if len(readyTasks) == 0 {
			fmt.Println("no ready tasks")
			return nil
		}

		selected, reason, err := task.SelectNextReady(ctx, tracker, readyTasks, policy)
		if err != nil {
			return err
		}
		fmt.Printf("selected %s (%s)\n", selected.ID, reason)

		if err := runTaskByID(ctx, tracker, runStore, runner, selected.ID); err != nil {
			if continueOnFail {
				fmt.Printf("task %s failed: %v\n", selected.ID, err)
				continue
			}
			return err
		}
	}
}

func recoverDoingTasks(ctx context.Context, tracker task.Tracker, runStore *run.Store, normaDir string) error {
	lock, ok, err := run.TryAcquireRunLock(normaDir)
	if err != nil {
		return err
	}
	if ok {
		defer lock.Release()
	}
	status := "doing"
	items, err := tracker.List(ctx, &status)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.RunID == nil {
			if err := tracker.MarkStatus(ctx, item.ID, "failed"); err != nil {
				return err
			}
			continue
		}
		runStatus, err := runStore.GetRunStatus(ctx, *item.RunID)
		if err != nil {
			return err
		}
		if runStatus != "running" || ok {
			if err := tracker.MarkStatus(ctx, item.ID, "failed"); err != nil {
				return err
			}
		}
	}
	return nil
}
