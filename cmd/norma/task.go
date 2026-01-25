package main

import (
	"context"
	"fmt"
	"strconv"
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
			storeDB, _, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx := cmd.Context()
			store := task.NewStore(storeDB)
			trimmedRunID := strings.TrimSpace(runID)
			var runIDPtr *string
			if trimmedRunID != "" {
				r := trimmedRunID
				runIDPtr = &r
			}
			ac := normalizeAC(acList)
			id, err := store.Add(ctx, goal, goal, ac, runIDPtr)
			if err != nil {
				return err
			}
			fmt.Printf("task %d added\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "associate task with a run")
	cmd.Flags().StringArrayVar(&acList, "ac", nil, "acceptance criterion text (repeatable)")
	return cmd
}

func taskListCmd() *cobra.Command {
	var status string
	var showAll bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, _, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()
			store := task.NewStore(storeDB)
			var statusPtr *string
			if showAll {
				statusPtr = nil
			} else if status != "" {
				statusPtr = &status
			} else {
				defaultStatus := "todo"
				statusPtr = &defaultStatus
			}
			items, err := store.List(context.Background(), statusPtr)
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
				fmt.Printf("%d\t%s\t%s\t%s\n", item.ID, item.Status, run, title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (todo|doing|done|failed|stopped)")
	cmd.Flags().BoolVar(&showAll, "all", false, "show all tasks")
	return cmd
}

func taskDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id")
			}
			storeDB, _, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()
			store := task.NewStore(storeDB)
			if err := store.MarkDone(context.Background(), id); err != nil {
				return err
			}
			fmt.Printf("task %d done\n", id)
			return nil
		},
	}
	return cmd
}

func taskLinkCmd() *cobra.Command {
	var dependsOn []int64
	cmd := &cobra.Command{
		Use:   "link <task-id>",
		Short: "Link a task to dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id")
			}
			if len(dependsOn) == 0 {
				return fmt.Errorf("at least one --depends-on id is required")
			}
			storeDB, _, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()
			store := task.NewStore(storeDB)
			for _, dep := range dependsOn {
				if dep == taskID {
					return fmt.Errorf("task cannot depend on itself")
				}
				if err := store.AddDependency(context.Background(), taskID, dep); err != nil {
					return err
				}
			}
			fmt.Printf("task %d linked\n", taskID)
			return nil
		},
	}
	cmd.Flags().Int64SliceVar(&dependsOn, "depends-on", nil, "task id this task depends on (repeatable)")
	return cmd
}

func runTaskByID(ctx context.Context, store *task.Store, runner *run.Runner, id int64) error {
	item, err := store.Get(ctx, id)
	if err != nil {
		return err
	}
	switch item.Status {
	case "todo", "failed", "stopped":
	case "doing":
		if item.RunID != nil {
			status, err := store.RunStatus(ctx, *item.RunID)
			if err != nil {
				return err
			}
			if status == "running" {
				return fmt.Errorf("task %d already running", id)
			}
		}
		if err := store.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("task %d status is %s", id, item.Status)
	}
	if err := store.MarkStatus(ctx, id, "doing"); err != nil {
		return err
	}
	result, err := runner.Run(ctx, item.Goal, item.Criteria)
	if err != nil {
		_ = store.MarkStatus(ctx, id, "failed")
		return err
	}
	if result.RunID != "" {
		_ = store.SetRun(ctx, id, result.RunID)
	}
	switch result.Status {
	case "passed":
		if err := store.MarkStatus(ctx, id, "done"); err != nil {
			return err
		}
		fmt.Printf("task %d passed (run %s)\n", id, result.RunID)
		return nil
	case "failed":
		if err := store.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
		return fmt.Errorf("task %d failed (run %s)", id, result.RunID)
	case "stopped":
		if err := store.MarkStatus(ctx, id, "stopped"); err != nil {
			return err
		}
		return fmt.Errorf("task %d stopped (run %s)", id, result.RunID)
	default:
		if err := store.MarkStatus(ctx, id, "failed"); err != nil {
			return err
		}
		return fmt.Errorf("task %d failed (run %s)", id, result.RunID)
	}
}

func runLeafTasks(ctx context.Context, store *task.Store, runner *run.Runner, continueOnFail bool) error {
	for {
		leafTasks, err := store.LeafTasks(ctx)
		if err != nil {
			return err
		}
		if len(leafTasks) == 0 {
			fmt.Println("no leaf tasks")
			return nil
		}
		doneCount := 0
		failCount := 0
		for _, item := range leafTasks {
			if err := runTaskByID(ctx, store, runner, item.ID); err != nil {
				failCount++
				if continueOnFail {
					fmt.Printf("task %d failed: %v\n", item.ID, err)
					continue
				}
				return err
			}
			doneCount++
		}
		if doneCount == 0 {
			if !continueOnFail && failCount > 0 {
				return fmt.Errorf("all leaf tasks failed")
			}
			return nil
		}
	}
}

func recoverDoingTasks(ctx context.Context, store *task.Store, normaDir string) error {
	lock, ok, err := run.TryAcquireRunLock(normaDir)
	if err != nil {
		return err
	}
	if ok {
		defer lock.Release()
	}
	status := "doing"
	items, err := store.List(ctx, &status)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.RunID == nil {
			if err := store.MarkStatus(ctx, item.ID, "failed"); err != nil {
				return err
			}
			continue
		}
		runStatus, err := store.RunStatus(ctx, *item.RunID)
		if err != nil {
			return err
		}
		if runStatus != "running" || ok {
			if err := store.MarkStatus(ctx, item.ID, "failed"); err != nil {
				return err
			}
		}
	}
	return nil
}
