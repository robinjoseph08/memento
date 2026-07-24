package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("MEMENTO_DATABASE_URL", "postgresql://memento:secret@db:5432/memento?sslmode=require")
	t.Setenv("MEMENTO_IMMICH_URL", "https://immich.internal")
	t.Setenv("MEMENTO_IMMICH_API_KEY", "private-key")
}

func TestLoadUsesDefaultsAndEnvironment(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("MEMENTO_HTTP_SHUTDOWN_TIMEOUT", "7s")
	t.Setenv("MEMENTO_DATABASE_MAX_OPEN_CONNS", "4")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8081", cfg.HTTP.Address)
	assert.Equal(t, 7*time.Second, cfg.HTTP.ShutdownTimeout)
	assert.Equal(t, 4, cfg.Database.MaxOpenConns)
}

func TestLoadPrecedenceIncludesYAMLAndSecretFiles(t *testing.T) {
	t.Setenv("MEMENTO_DATABASE_URL", "postgresql://memento:env@db:5432/memento")
	t.Setenv("MEMENTO_IMMICH_URL", "https://environment.example")
	secretPath := filepath.Join(t.TempDir(), "immich-key")
	require.NoError(t, os.WriteFile(secretPath, []byte("file-key\n"), 0o600))
	t.Setenv("MEMENTO_IMMICH_API_KEY_FILE", secretPath)
	configPath := filepath.Join(t.TempDir(), "memento.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
http:
  address: ":9000"
database:
  url: "postgresql://memento:yaml@db:5432/memento"
immich:
  url: "https://yaml.example"
  api_key: "yaml-key"
`), 0o600))

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, ":9000", cfg.HTTP.Address)
	assert.Contains(t, cfg.Database.URL, ":env@")
	assert.Equal(t, "https://environment.example", cfg.Immich.URL)
	assert.Equal(t, "file-key", cfg.Immich.APIKey)
}

func TestLoadRejectsUnreadableAndEmptySecretFiles(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("MEMENTO_DATABASE_URL_FILE", filepath.Join(t.TempDir(), "missing"))
	_, err := Load("")
	require.ErrorContains(t, err, "MEMENTO_DATABASE_URL_FILE")

	t.Setenv("MEMENTO_DATABASE_URL_FILE", "")
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	t.Setenv("MEMENTO_IMMICH_API_KEY_FILE", empty)
	_, err = Load("")
	require.ErrorContains(t, err, "file is empty")
}

func TestLoadRejectsMissingConfigurationFile(t *testing.T) {
	setRequiredEnvironment(t)
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "load configuration file")
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("MEMENTO_WORKER_POLL_INTERVAL", "eventually")
	_, err := Load("")
	require.EqualError(t, err, "worker.poll_interval must be a positive duration")
}

func TestLoadSecureSMTPAndPasswordFile(t *testing.T) {
	setRequiredEnvironment(t)
	passwordPath := filepath.Join(t.TempDir(), "smtp-password")
	require.NoError(t, os.WriteFile(passwordPath, []byte("smtp-secret\n"), 0o600))
	t.Setenv("MEMENTO_SMTP_ENABLED", "true")
	t.Setenv("MEMENTO_SMTP_HOST", "smtp.example.com")
	t.Setenv("MEMENTO_SMTP_PORT", "465")
	t.Setenv("MEMENTO_SMTP_MODE", "implicit_tls")
	t.Setenv("MEMENTO_SMTP_USERNAME", "mailer")
	t.Setenv("MEMENTO_SMTP_PASSWORD_FILE", passwordPath)
	t.Setenv("MEMENTO_SMTP_FROM_ADDRESS", "memento@example.com")
	t.Setenv("MEMENTO_SMTP_TEST_RECIPIENT", "operator@example.com")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.True(t, cfg.SMTP.Enabled)
	assert.Equal(t, "smtp-secret", cfg.SMTP.Password)
	assert.Equal(t, "implicit_tls", cfg.SMTP.Mode)
}

func TestValidateRejectsUnsafeSMTP(t *testing.T) {
	setRequiredEnvironment(t)
	valid, err := Load("")
	require.NoError(t, err)
	valid.SMTP = SMTPConfig{
		Enabled: true, Host: "smtp.example.com", Port: 587, Mode: "starttls",
		FromAddress: "memento@example.com", TestRecipient: "operator@example.com",
		Timeout: time.Second, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: time.Hour,
	}

	tests := []struct {
		name string
		edit func(*SMTPConfig)
		want string
	}{
		{"missing host", func(c *SMTPConfig) { c.Host = "" }, "smtp.host"},
		{"invalid port", func(c *SMTPConfig) { c.Port = 0 }, "smtp.port"},
		{"invalid mode", func(c *SMTPConfig) { c.Mode = "plaintext" }, "smtp.mode"},
		{"partial credentials", func(c *SMTPConfig) { c.Username = "mailer" }, "both be set"},
		{"invalid sender", func(c *SMTPConfig) { c.FromAddress = "not-an-email" }, "single email"},
		{"plaintext opt in", func(c *SMTPConfig) { c.Mode = "insecure" }, "insecure_development"},
		{"plaintext public host", func(c *SMTPConfig) { c.Mode = "insecure"; c.InsecureDevelopment = true }, "loopback or private"},
		{"plaintext credentials", func(c *SMTPConfig) {
			c.Mode = "insecure"
			c.InsecureDevelopment = true
			c.Host = "127.0.0.1"
			c.Username = "mailer"
			c.Password = "secret"
		}, "credentials are not permitted"},
		{"secure with insecure warning", func(c *SMTPConfig) { c.InsecureDevelopment = true }, "permitted only"},
		{"retry bounds", func(c *SMTPConfig) { c.RetryBase = 2 * time.Hour }, "retry durations"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate.SMTP)
			assert.ErrorContains(t, candidate.Validate(), test.want)
		})
	}
}

func TestValidateAcceptsExplicitLoopbackSMTPForDevelopment(t *testing.T) {
	setRequiredEnvironment(t)
	cfg, err := Load("")
	require.NoError(t, err)
	cfg.SMTP = SMTPConfig{
		Enabled: true, Host: "127.0.0.1", Port: 1025, Mode: "insecure", InsecureDevelopment: true,
		FromAddress: "memento@example.com", TestRecipient: "operator@example.com",
		Timeout: time.Second, RetryBase: time.Second, RetryMax: time.Minute, RetryWindow: time.Hour,
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidateRejectsUnsafeValues(t *testing.T) {
	setRequiredEnvironment(t)
	valid, err := Load("")
	require.NoError(t, err)

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"HTTP address", func(c *Config) { c.HTTP.Address = "" }, "http.address is required"},
		{"database URL", func(c *Config) { c.Database.URL = "" }, "database.url is required"},
		{"database name", func(c *Config) { c.Database.Name = "" }, "database.name is required"},
		{"connection count", func(c *Config) { c.Database.MaxOpenConns = 1 }, "database.max_open_conns must be at least 2"},
		{"database scheme", func(c *Config) { c.Database.URL = "mysql://db/memento" }, "database.url must be a PostgreSQL URL"},
		{"database path", func(c *Config) { c.Database.URL = "postgresql://db" }, "database.url must select one logical database"},
		{"wrong logical database", func(c *Config) { c.Database.URL = "postgresql://db/immich" }, "must select the configured Memento database"},
		{"Immich URL", func(c *Config) { c.Immich.URL = "" }, "immich.url is required"},
		{"Immich credentials", func(c *Config) { c.Immich.URL = "https://user:pass@immich.example" }, "without credentials"},
		{"Immich key", func(c *Config) { c.Immich.APIKey = "" }, "immich.api_key is required"},
		{"heartbeat", func(c *Config) { c.Worker.HeartbeatMaxAge = c.Worker.HeartbeatInterval }, "heartbeat_max_age"},
		{"poll lease", func(c *Config) { c.Worker.LeaseDuration = c.Worker.PollInterval }, "lease_duration"},
		{"heartbeat lease", func(c *Config) { c.Worker.LeaseDuration = c.Worker.HeartbeatInterval }, "heartbeat_interval"},
		{"worker retry bounds", func(c *Config) { c.Worker.RetryBase = 2 * c.Worker.RetryMax }, "worker.retry_base"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			assert.ErrorContains(t, candidate.Validate(), test.want)
		})
	}
}

func TestErrorsDoNotContainSecrets(t *testing.T) {
	setRequiredEnvironment(t)
	secret := "never-print-this"
	t.Setenv("MEMENTO_DATABASE_URL", "postgresql://memento:"+secret+"@db:5432/immich")
	_, err := Load("")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}
