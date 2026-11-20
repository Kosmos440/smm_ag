package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"smmagent/internal/postgres"
)

func drainEventCatalog(ctx context.Context, store *postgres.Store, scope string, channelID int64, from, until time.Time) (int, error) {
	count := 0
	for {
		items, err := store.CatalogMaterials(ctx, scope, channelID, from, until)
		if err != nil {
			return count, err
		}
		for _, item := range items {
			if err := store.CatalogMaterial(ctx, scope, item); err != nil {
				return count, fmt.Errorf("catalog material %d: %w", item.ID, err)
			}
		}
		count += len(items)
		if len(items) == 0 {
			return count, nil
		}
	}
}

func followEventCatalog(ctx context.Context, store *postgres.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		now := time.Now()
		if _, err := drainEventCatalog(ctx, store, "shadow", 0, now.Add(-72*time.Hour), now); err != nil && ctx.Err() == nil {
			log.Printf("shadow event catalog: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func replayEventCatalog(ctx context.Context, store *postgres.Store, args []string) error {
	flags := flag.NewFlagSet("events-replay", flag.ContinueOnError)
	date := flags.String("date", "", "archive date in Europe/Moscow (YYYY-MM-DD)")
	channelID := flags.Int64("channel", 0, "channel ID")
	show := flags.String("show", "", "comma-separated material IDs to include in the JSON report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *channelID <= 0 || flags.NArg() != 0 {
		return errors.New("events-replay requires -date and a positive -channel")
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return err
	}
	from, err := time.ParseInLocation(time.DateOnly, *date, location)
	if err != nil {
		return fmt.Errorf("invalid archive date: %w", err)
	}
	var ids []int64
	if *show != "" {
		for _, value := range strings.Split(*show, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid material ID %q", value)
			}
			ids = append(ids, id)
		}
	}
	scope := fmt.Sprintf("archive/%s/%d", *date, *channelID)
	count, err := drainEventCatalog(ctx, store, scope, *channelID, from, from.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	assignments, err := store.CatalogAssignments(ctx, scope, ids)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Scope       string                       `json:"scope"`
		Processed   int                          `json:"new_materials"`
		Assignments []postgres.CatalogAssignment `json:"assignments"`
	}{scope, count, assignments})
}
