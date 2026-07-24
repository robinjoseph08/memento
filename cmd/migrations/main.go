package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/migrations"
)

var errUsage = errors.New("usage: migrations apply|validate")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 || (args[0] != "apply" && args[0] != "validate") {
		return errUsage
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	if args[0] == "apply" {
		if err := migrations.Apply(ctx, db); err != nil {
			return err
		}
	}
	if err := migrations.Extensions(ctx, db); err != nil {
		return err
	}
	if err := migrations.Current(ctx, db); err != nil {
		return err
	}
	return migrations.SetupConsistent(ctx, db)
}
