//go:build !integration

package main

import (
	"context"
	"errors"

	"github.com/uptrace/bun"
)

var errIntegrationTagRequired = errors.New("browser fixture requires the integration build tag")

func openFixtureDatabase(context.Context, string) (*bun.DB, error) {
	return nil, errIntegrationTagRequired
}
