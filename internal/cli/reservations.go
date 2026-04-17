// internal/cli/reservations.go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"
)

type reservationResp struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Project  *string    `json:"project,omitempty"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

// ReservationsCommand returns the relay reservations Command.
// Subcommands: list, create, delete
func ReservationsCommand() Command {
	return Command{
		Name:  "reservations",
		Usage: "reservations <list|create|delete> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doReservations(ctx, cfg, args, os.Stdout)
		},
	}
}

func doReservations(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay reservations <list|create|delete>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	switch args[0] {
	case "list":
		return doReservationsList(ctx, c, args[1:], w)
	case "create":
		return doReservationsCreate(ctx, c, args[1:], w)
	case "delete":
		return doReservationsDelete(ctx, c, args[1:], w)
	default:
		return fmt.Errorf("unknown reservations subcommand: %s", args[0])
	}
}

func doReservationsList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	var reservations []reservationResp
	if err := c.do(ctx, "GET", "/v1/reservations", nil, &reservations); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPROJECT\tSTARTS\tENDS")
	for _, res := range reservations {
		project := ""
		if res.Project != nil {
			project = *res.Project
		}
		starts, ends := "", ""
		if res.StartsAt != nil {
			starts = res.StartsAt.Format("2006-01-02")
		}
		if res.EndsAt != nil {
			ends = res.EndsAt.Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", res.ID, res.Name, project, starts, ends)
	}
	return tw.Flush()
}

func doReservationsCreate(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay reservations create <reservation.json>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var res reservationResp
	if err := c.do(ctx, "POST", "/v1/reservations", body, &res); err != nil {
		return err
	}
	fmt.Fprintf(w, "Reservation %s created: %s\n", res.ID, res.Name)
	return nil
}

func doReservationsDelete(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay reservations delete <reservation-id>")
	}
	if err := c.do(ctx, "DELETE", "/v1/reservations/"+args[0], nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "Reservation %s deleted.\n", args[0])
	return nil
}
