//go:build integration

package main

import (
	"context"
	"net"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
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
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	t.Setenv("MEMENTO_SMTP_ENABLED", "true")
	t.Setenv("MEMENTO_SMTP_HOST", host)
	t.Setenv("MEMENTO_SMTP_PORT", port)
	t.Setenv("MEMENTO_SMTP_MODE", "insecure")
	t.Setenv("MEMENTO_SMTP_INSECURE_DEVELOPMENT", "true")
	t.Setenv("MEMENTO_SMTP_FROM_ADDRESS", "memento@example.com")
	t.Setenv("MEMENTO_SMTP_TEST_RECIPIENT", "operator@example.com")
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	t.Setenv("MEMENTO_PUSH_ENABLED", "true")
	t.Setenv("MEMENTO_PUSH_PUBLIC_KEY", publicKey)
	t.Setenv("MEMENTO_PUSH_PRIVATE_KEY", privateKey)
	t.Setenv("MEMENTO_PUSH_SUBJECT", "mailto:operator@example.com")
	var contacts atomic.Int32
	acceptDone := make(chan struct{})
	var cleanupOnce sync.Once
	cleanupListener := func() {
		cleanupOnce.Do(func() {
			_ = listener.Close()
			<-acceptDone
		})
	}
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
	t.Cleanup(cleanupListener)

	require.NoError(t, run([]string{"validate"}))
	cleanupListener()
	assert.Zero(t, contacts.Load(), "restore validation contacted a configured external dependency")
}
