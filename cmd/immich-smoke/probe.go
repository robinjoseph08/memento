package main

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/immich"
)

// probeSource skips only version policy in the disposable compatibility command.
// All source reads still execute the shipped adapter without translating responses.
type probeSource struct{ *immich.Client }

func (probeSource) CheckImport(context.Context) error { return nil }
