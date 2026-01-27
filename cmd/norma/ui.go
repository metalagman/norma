package main

import (
	"fmt"
	"net/http"

	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/web"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func uiCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Start the web UI",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, _, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()

			tracker := task.NewBeadsTracker("")
			server, err := web.NewServer(tracker)
			if err != nil {
				return err
			}

			addr := fmt.Sprintf(":%d", port)
			log.Info().Msgf("Starting UI on http://localhost%s", addr)
			return http.ListenAndServe(addr, server.Routes())
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on")
	return cmd
}
