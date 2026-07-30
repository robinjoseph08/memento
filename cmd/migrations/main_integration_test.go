//go:build integration

package main

import (
	"context"
	"net"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCommandMakesNoExternalContact(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db))
	var schema string
	require.NoError(t, db.NewRaw(`SELECT current_schema()`).Scan(ctx, &schema))

	databaseURL, err := url.Parse(os.Getenv("MEMENTO_TEST_DATABASE_URL"))
	require.NoError(t, err)
	query := databaseURL.Query()
	query.Set("search_path", schema)
	databaseURL.RawQuery = query.Encode()
	t.Setenv("MEMENTO_DATABASE_URL", databaseURL.String())
	t.Setenv("MEMENTO_DATABASE_NAME", "memento")
	t.Setenv("MEMENTO_HTTP_PUBLIC_URL", "https://memento.example")
	t.Setenv("MEMENTO_IMMICH_API_KEY", "validation-must-not-use-this-key")
	t.Setenv("MEMENTO_SECURITY_SECRET", "validation-command-security-secret-32-bytes")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Setenv("MEMENTO_IMMICH_URL", "http://"+listener.Addr().String())
	var contacts atomic.Int32
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			contacts.Add(1)
			_ = connection.Close()
		}
	}()

	require.NoError(t, run([]string{"validate"}))
	require.NoError(t, listener.Close())
	<-acceptDone
	assert.Zero(t, contacts.Load(), "restore validation contacted a configured external dependency")
}
