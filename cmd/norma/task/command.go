package taskcmd

import (
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command builds the `norma task` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage norma tasks",
	}
	cmd.AddCommand(addCommand())
	cmd.AddCommand(listCommand())
	cmd.AddCommand(doneCommand())
	cmd.AddCommand(linkCommand())
	return cmd
}

func addCommand() *cobra.Command {
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
			log.Info().Msgf("task %s added", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "associate task with a run")
	cmd.Flags().StringArrayVar(&acList, "ac", nil, "acceptance criterion text (repeatable)")
	return cmd
}

func listCommand() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tracker := task.NewBeadsTracker("")
			var statusPtr *string
			if status != "" {
				statusPtr = &status
			}
			items, err := tracker.List(cmd.Context(), statusPtr)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				log.Info().Msg("no tasks")
				return nil
			}
			for _, item := range items {
				runID := "-"
				if item.RunID != nil {
					runID = *item.RunID
				}
				title := item.Title
				if title == "" {
					title = item.Goal
				}
				fmt.Printf("%s\t%s\t%s\t%s\n", item.ID, item.Status, runID, title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (todo|doing|done|failed|stopped)")
	return cmd
}

func doneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			if err := tracker.MarkDone(cmd.Context(), id); err != nil {
				return err
			}
			log.Info().Msgf("task %s done", id)
			return nil
		},
	}
}

func linkCommand() *cobra.Command {
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
				if err := tracker.AddDependency(cmd.Context(), taskID, dep); err != nil {
					return err
				}
			}
			log.Info().Msgf("task %s linked", taskID)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&dependsOn, "depends-on", nil, "task id this task depends on (repeatable)")
	return cmd
}

func normalizeAC(texts []string) []task.AcceptanceCriterion {
	if len(texts) == 0 {
		return nil
	}
	out := make([]task.AcceptanceCriterion, 0, len(texts))
	for i, text := range texts {
		id := fmt.Sprintf("AC%d", i+1)
		out = append(out, task.AcceptanceCriterion{ID: id, Text: text})
	}
	return out
}
