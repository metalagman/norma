package taskscmd

import (
	"fmt"

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
