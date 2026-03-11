package taskscmd

import (
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
)

// Command builds the `norma tasks` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Manage norma tasks via Beads",
	}
	cmd.AddCommand(listCommand())
	cmd.AddCommand(addCommand())
	cmd.AddCommand(showCommand())
	cmd.AddCommand(statusCommand())
	cmd.AddCommand(deleteCommand())
	cmd.AddCommand(updateCommand())
	cmd.AddCommand(depCommand())
	cmd.AddCommand(selectCommand())
	cmd.AddCommand(notesCommand())
	return cmd
}

func notesCommand() *cobra.Command {
	var set bool
	cmd := &cobra.Command{
		Use:   "notes <id> [text]",
		Short: "Show or set task notes (e.g. TaskState JSON)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			if len(args) == 2 && set {
				notes := args[1]
				if err := tracker.SetNotes(cmd.Context(), id, notes); err != nil {
					return err
				}
				fmt.Printf("Updated notes for task %s\n", id)
				return nil
			}

			t, err := tracker.Task(cmd.Context(), id)
			if err != nil {
				return err
			}
			fmt.Println(t.Notes)
			return nil
		},
	}
	cmd.Flags().BoolVar(&set, "set", false, "Set notes instead of showing")
	return cmd
}

func selectCommand() *cobra.Command {
	var featureID string
	var epicID string
	cmd := &cobra.Command{
		Use:   "select",
		Short: "Show the next task that would be selected by the scheduler",
		RunE: func(cmd *cobra.Command, args []string) error {
			tracker := task.NewBeadsTracker("")
			ready, err := tracker.LeafTasks(cmd.Context())
			if err != nil {
				return err
			}
			if len(ready) == 0 {
				fmt.Println("No ready tasks found.")
				return nil
			}

			policy := task.SelectionPolicy{
				ActiveFeatureID: featureID,
				ActiveEpicID:    epicID,
			}

			selected, reason, err := task.SelectNextReady(cmd.Context(), tracker, ready, policy)
			if err != nil {
				return err
			}

			fmt.Printf("Selected: %s (%s)\n", selected.ID, selected.Title)
			fmt.Printf("Reason:   %s\n", reason)
			return nil
		},
	}
	cmd.Flags().StringVar(&featureID, "feature", "", "Active feature ID for selection")
	cmd.Flags().StringVar(&epicID, "epic", "", "Active epic ID for selection")
	return cmd
}

func addCommand() *cobra.Command {
	var taskType string
	var parentID string
	var goal string
	var criteria []string
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task, epic, or feature to Beads",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			tracker := task.NewBeadsTracker("")
			var id string
			var err error

			var acs []task.AcceptanceCriterion
			for _, c := range criteria {
				acs = append(acs, task.AcceptanceCriterion{Text: c})
			}

			switch taskType {
			case "epic":
				id, err = tracker.AddEpic(cmd.Context(), title, goal)
			case "feature":
				if parentID == "" {
					return fmt.Errorf("parent ID is required for features")
				}
				id, err = tracker.AddFeature(cmd.Context(), parentID, title)
			default:
				id, err = tracker.AddTaskDetailed(cmd.Context(), parentID, title, goal, acs, nil)
			}

			if err != nil {
				return err
			}

			fmt.Printf("Created %s %s\n", taskType, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskType, "type", "task", "Type of task (task, epic, feature)")
	cmd.Flags().StringVar(&parentID, "parent", "", "Parent task ID")
	cmd.Flags().StringVar(&goal, "goal", "", "Goal/description of the task")
	cmd.Flags().StringSliceVar(&criteria, "criteria", nil, "Acceptance criteria")
	return cmd
}

func updateCommand() *cobra.Command {
	var title string
	var goal string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update task title and goal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			t, err := tracker.Task(cmd.Context(), id)
			if err != nil {
				return err
			}
			if title == "" {
				title = t.Title
			}
			if goal == "" {
				goal = t.Goal
			}
			if err := tracker.Update(cmd.Context(), id, title, goal); err != nil {
				return err
			}
			fmt.Printf("Updated task %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&goal, "goal", "", "New goal/description")
	return cmd
}

func depCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep <task-id> <depends-on-id>",
		Short: "Add a dependency between tasks",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			dependsOnID := args[1]
			tracker := task.NewBeadsTracker("")
			if err := tracker.AddDependency(cmd.Context(), taskID, dependsOnID); err != nil {
				return err
			}
			fmt.Printf("Added dependency: %s depends on %s\n", taskID, dependsOnID)
			return nil
		},
	}
	return cmd
}

func showCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			t, err := tracker.Task(cmd.Context(), id)
			if err != nil {
				return err
			}

			fmt.Printf("ID:       %s\n", t.ID)
			fmt.Printf("Title:    %s\n", t.Title)
			fmt.Printf("Type:     %s\n", t.Type)
			fmt.Printf("Status:   %s\n", t.Status)
			if t.ParentID != "" {
				fmt.Printf("Parent:   %s\n", t.ParentID)
			}
			if t.Goal != "" {
				fmt.Printf("Goal:     %s\n", t.Goal)
			}
			if len(t.Criteria) > 0 {
				fmt.Println("Criteria:")
				for _, c := range t.Criteria {
					fmt.Printf("  - [%s] %s\n", c.ID, c.Text)
				}
			}
			if len(t.Labels) > 0 {
				fmt.Printf("Labels:   %s\n", strings.Join(t.Labels, ", "))
			}
			return nil
		},
	}
	return cmd
}

func statusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <id> <status>",
		Short: "Update task status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			status := args[1]
			tracker := task.NewBeadsTracker("")
			if err := tracker.MarkStatus(cmd.Context(), id, status); err != nil {
				return err
			}
			fmt.Printf("Updated task %s status to %s\n", id, status)
			return nil
		},
	}
	return cmd
}

func deleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			tracker := task.NewBeadsTracker("")
			if err := tracker.Delete(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Printf("Deleted task %s\n", id)
			return nil
		},
	}
	return cmd
}

func listCommand() *cobra.Command {
	var status string
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks from Beads",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tracker := task.NewBeadsTracker("")
			var tasks []task.Task
			var err error

			switch {
			case all:
				tasks, err = tracker.List(cmd.Context(), nil)
			case status != "":
				tasks, err = tracker.List(cmd.Context(), &status)
			default:
				// Default to ready tasks
				tasks, err = tracker.LeafTasks(cmd.Context())
			}

			if err != nil {
				return err
			}

			if len(tasks) == 0 {
				fmt.Println("No tasks found.")
				return nil
			}

			fmt.Printf("%-20s %-10s %-10s %s\n", "ID", "STATUS", "TYPE", "TITLE")
			fmt.Printf("%s\n", "--------------------------------------------------------------------------------")
			for _, t := range tasks {
				fmt.Printf("%-20s %-10s %-10s %s\n", t.ID, t.Status, t.Type, t.Title)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (todo, doing, done, failed, stopped)")
	cmd.Flags().BoolVar(&all, "all", false, "List all tasks")
	return cmd
}
