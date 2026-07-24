// Package config loads and validates portable Memento configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const envPrefix = "MEMENTO_"

// Config is the validated runtime configuration. Secret values must never be logged.
type Config struct {
	HTTP     HTTPConfig
	Database DatabaseConfig
	Immich   ImmichConfig
	Worker   WorkerConfig
}

type HTTPConfig struct {
	Address         string
	ShutdownTimeout time.Duration
}

type DatabaseConfig struct {
	URL           string
	Name          string
	MaxOpenConns  int
	HealthTimeout time.Duration
}

type ImmichConfig struct {
	URL           string
	APIKey        string
	HealthTimeout time.Duration
}

type WorkerConfig struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	HeartbeatMaxAge   time.Duration
	LeaseDuration     time.Duration
	DrainTimeout      time.Duration
}

type rawConfig struct {
	HTTP struct {
		Address         string `koanf:"address"`
		ShutdownTimeout string `koanf:"shutdown_timeout"`
	} `koanf:"http"`
	Database struct {
		URL           string `koanf:"url"`
		Name          string `koanf:"name"`
		MaxOpenConns  int    `koanf:"max_open_conns"`
		HealthTimeout string `koanf:"health_timeout"`
	} `koanf:"database"`
	Immich struct {
		URL           string `koanf:"url"`
		APIKey        string `koanf:"api_key"`
		HealthTimeout string `koanf:"health_timeout"`
	} `koanf:"immich"`
	Worker struct {
		PollInterval      string `koanf:"poll_interval"`
		HeartbeatInterval string `koanf:"heartbeat_interval"`
		HeartbeatMaxAge   string `koanf:"heartbeat_max_age"`
		LeaseDuration     string `koanf:"lease_duration"`
		DrainTimeout      string `koanf:"drain_timeout"`
	} `koanf:"worker"`
}

var (
	errSecretFileEmpty          = errors.New("file is empty")
	errPositiveDuration         = errors.New("must be a positive duration")
	errHTTPAddressRequired      = errors.New("http.address is required")
	errDatabaseURLRequired      = errors.New("database.url is required")
	errDatabaseNameRequired     = errors.New("database.name is required")
	errDatabaseConnections      = errors.New("database.max_open_conns must be at least 2 for the migration lock")
	errDatabaseURLInvalid       = errors.New("database.url must be a PostgreSQL URL")
	errDatabaseSelectionInvalid = errors.New("database.url must select one logical database")
	errDatabaseNameMismatch     = errors.New("database.url must select the configured Memento database")
	errImmichURLRequired        = errors.New("immich.url is required")
	errImmichURLInvalid         = errors.New("immich.url must be an HTTP URL without credentials")
	errImmichAPIKeyRequired     = errors.New("immich.api_key is required")
	errHeartbeatMaxAge          = errors.New("worker.heartbeat_max_age must exceed worker.heartbeat_interval")
	errLeasePollInterval        = errors.New("worker.lease_duration must exceed worker.poll_interval")
	errLeaseHeartbeatInterval   = errors.New("worker.lease_duration must exceed worker.heartbeat_interval")
)

var defaults = map[string]any{
	"http.address":              "127.0.0.1:8081",
	"http.shutdown_timeout":     "8s",
	"database.name":             "memento",
	"database.max_open_conns":   10,
	"database.health_timeout":   "2s",
	"immich.health_timeout":     "2s",
	"worker.poll_interval":      "1s",
	"worker.heartbeat_interval": "2s",
	"worker.heartbeat_max_age":  "10s",
	"worker.lease_duration":     "30s",
	"worker.drain_timeout":      "5s",
}

// Load reads defaults, an optional YAML file, environment variables, and secret files in that order.
// The explicit path takes precedence over MEMENTO_CONFIG_FILE.
func Load(path string) (Config, error) {
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}
	if path == "" {
		path = os.Getenv("MEMENTO_CONFIG_FILE")
	}
	if path != "" {
		if err := k.Load(file.Provider(filepath.Clean(path)), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load configuration file: %w", err)
		}
	}
	if err := k.Load(env.Provider(envPrefix, ".", envKey), nil); err != nil {
		return Config{}, fmt.Errorf("load environment: %w", err)
	}
	if err := loadSecretFile(k, "database.url", "MEMENTO_DATABASE_URL_FILE"); err != nil {
		return Config{}, err
	}
	if err := loadSecretFile(k, "immich.api_key", "MEMENTO_IMMICH_API_KEY_FILE"); err != nil {
		return Config{}, err
	}

	var raw rawConfig
	if err := k.Unmarshal("", &raw); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	cfg, err := parse(raw)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envKey(key string) string {
	known := map[string]string{
		"MEMENTO_HTTP_ADDRESS":              "http.address",
		"MEMENTO_HTTP_SHUTDOWN_TIMEOUT":     "http.shutdown_timeout",
		"MEMENTO_DATABASE_URL":              "database.url",
		"MEMENTO_DATABASE_NAME":             "database.name",
		"MEMENTO_DATABASE_MAX_OPEN_CONNS":   "database.max_open_conns",
		"MEMENTO_DATABASE_HEALTH_TIMEOUT":   "database.health_timeout",
		"MEMENTO_IMMICH_URL":                "immich.url",
		"MEMENTO_IMMICH_API_KEY":            "immich.api_key",
		"MEMENTO_IMMICH_HEALTH_TIMEOUT":     "immich.health_timeout",
		"MEMENTO_WORKER_POLL_INTERVAL":      "worker.poll_interval",
		"MEMENTO_WORKER_HEARTBEAT_INTERVAL": "worker.heartbeat_interval",
		"MEMENTO_WORKER_HEARTBEAT_MAX_AGE":  "worker.heartbeat_max_age",
		"MEMENTO_WORKER_LEASE_DURATION":     "worker.lease_duration",
		"MEMENTO_WORKER_DRAIN_TIMEOUT":      "worker.drain_timeout",
	}
	if transformed, ok := known[key]; ok {
		return transformed
	}
	return strings.ToLower(strings.TrimPrefix(key, envPrefix))
}

func loadSecretFile(k *koanf.Koanf, key, environment string) error {
	path := os.Getenv(environment)
	if path == "" {
		return nil
	}
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read %s: %w", environment, err)
	}
	value := strings.TrimSpace(string(contents))
	if value == "" {
		return fmt.Errorf("read %s: %w", environment, errSecretFileEmpty)
	}
	if err := k.Set(key, value); err != nil {
		return fmt.Errorf("set %s: %w", environment, err)
	}
	return nil
}

func parse(raw rawConfig) (Config, error) {
	var cfg Config
	cfg.HTTP.Address = raw.HTTP.Address
	cfg.Database.URL = raw.Database.URL
	cfg.Database.Name = raw.Database.Name
	cfg.Database.MaxOpenConns = raw.Database.MaxOpenConns
	cfg.Immich.URL = raw.Immich.URL
	cfg.Immich.APIKey = raw.Immich.APIKey

	var err error
	if cfg.HTTP.ShutdownTimeout, err = duration("http.shutdown_timeout", raw.HTTP.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.Database.HealthTimeout, err = duration("database.health_timeout", raw.Database.HealthTimeout); err != nil {
		return Config{}, err
	}
	if cfg.Immich.HealthTimeout, err = duration("immich.health_timeout", raw.Immich.HealthTimeout); err != nil {
		return Config{}, err
	}
	if cfg.Worker.PollInterval, err = duration("worker.poll_interval", raw.Worker.PollInterval); err != nil {
		return Config{}, err
	}
	if cfg.Worker.HeartbeatInterval, err = duration("worker.heartbeat_interval", raw.Worker.HeartbeatInterval); err != nil {
		return Config{}, err
	}
	if cfg.Worker.HeartbeatMaxAge, err = duration("worker.heartbeat_max_age", raw.Worker.HeartbeatMaxAge); err != nil {
		return Config{}, err
	}
	if cfg.Worker.LeaseDuration, err = duration("worker.lease_duration", raw.Worker.LeaseDuration); err != nil {
		return Config{}, err
	}
	if cfg.Worker.DrainTimeout, err = duration("worker.drain_timeout", raw.Worker.DrainTimeout); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func duration(name, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s %w", name, errPositiveDuration)
	}
	return d, nil
}

// Validate rejects unsafe or incomplete runtime configuration.
func (c Config) Validate() error {
	if c.HTTP.Address == "" {
		return errHTTPAddressRequired
	}
	if c.Database.URL == "" {
		return errDatabaseURLRequired
	}
	if c.Database.Name == "" {
		return errDatabaseNameRequired
	}
	if c.Database.MaxOpenConns < 2 {
		return errDatabaseConnections
	}
	databaseURL, err := url.Parse(c.Database.URL)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Host == "" {
		return errDatabaseURLInvalid
	}
	actualName := strings.TrimPrefix(databaseURL.EscapedPath(), "/")
	actualName, err = url.PathUnescape(actualName)
	if err != nil || actualName == "" || strings.Contains(actualName, "/") {
		return errDatabaseSelectionInvalid
	}
	if actualName != c.Database.Name {
		return fmt.Errorf("%w %q", errDatabaseNameMismatch, c.Database.Name)
	}
	if c.Immich.URL == "" {
		return errImmichURLRequired
	}
	immichURL, err := url.Parse(c.Immich.URL)
	if err != nil || (immichURL.Scheme != "http" && immichURL.Scheme != "https") || immichURL.Host == "" || immichURL.User != nil {
		return errImmichURLInvalid
	}
	if c.Immich.APIKey == "" {
		return errImmichAPIKeyRequired
	}
	if c.Worker.HeartbeatMaxAge <= c.Worker.HeartbeatInterval {
		return errHeartbeatMaxAge
	}
	if c.Worker.LeaseDuration <= c.Worker.PollInterval {
		return errLeasePollInterval
	}
	if c.Worker.LeaseDuration <= c.Worker.HeartbeatInterval {
		return errLeaseHeartbeatInterval
	}
	return nil
}
