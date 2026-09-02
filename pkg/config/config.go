package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const defaultConfigPath = "/config/app.yaml"

// Config holds the application configuration. Values can come from a YAML
// file or environment variables. Environment variables take precedence.
type Config struct {
	DatabaseURL               string        `koanf:"database_url" json:"database_url" validate:"required"`
	DatabaseDebug             bool          `koanf:"database_debug" json:"database_debug"`
	DatabaseConnectRetryCount int           `koanf:"database_connect_retry_count" json:"database_connect_retry_count" validate:"min=1"`
	DatabaseConnectRetryDelay time.Duration `koanf:"database_connect_retry_delay" json:"database_connect_retry_delay" validate:"min=0"`
	FilesPath                 string        `koanf:"files_path" json:"files_path" validate:"required"`
	ServerHost                string        `koanf:"server_host" json:"server_host" validate:"required"`
	ServerPort                int           `koanf:"server_port" json:"server_port" validate:"min=0,max=65535"`
	Hostname                  string        `koanf:"-" json:"-"`
}

func defaults() *Config {
	return &Config{
		DatabaseURL:               "postgres://postgres:postgres@localhost:5432/memento?sslmode=disable",
		DatabaseDebug:             false,
		DatabaseConnectRetryCount: 5,
		DatabaseConnectRetryDelay: 2 * time.Second,
		FilesPath:                 "./tmp/files",
		ServerHost:                "0.0.0.0",
		ServerPort:                3579,
	}
}

// New loads the configuration from defaults, a YAML file, and environment
// variables, in that order.
func New() (*Config, error) {
	configPath := os.Getenv("CONFIG_FILE")
	requireConfigFile := configPath != ""
	if configPath == "" {
		configPath = defaultConfigPath
	}

	return load(configPath, requireConfigFile, env.Provider("", ".", strings.ToLower), os.Hostname)
}

func load(configPath string, requireConfigFile bool, environment koanf.Provider, hostname func() (string, error)) (*Config, error) {
	k := koanf.New(".")
	cfg := defaults()

	if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
		if !os.IsNotExist(err) || requireConfigFile {
			return nil, fmt.Errorf("load config file %s: %w", configPath, err)
		}
	}

	if environment != nil {
		if err := k.Load(environment, nil); err != nil {
			return nil, fmt.Errorf("load environment variables: %w", err)
		}
	}

	if err := k.Unmarshal("", cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	name, err := hostname()
	if err != nil {
		return nil, fmt.Errorf("get hostname: %w", err)
	}
	cfg.Hostname = name

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// NewForTest returns configuration suitable for unit tests that do not connect
// to a database.
func NewForTest() *Config {
	cfg := defaults()
	cfg.DatabaseURL = "postgres://test:test@localhost:5432/memento_test?sslmode=disable"
	cfg.DatabaseConnectRetryCount = 1
	cfg.DatabaseConnectRetryDelay = 0
	cfg.DatabaseDebug = true
	cfg.ServerHost = "127.0.0.1"
	cfg.ServerPort = 0
	cfg.Hostname = "test-host"
	return cfg
}

func validateConfig(cfg *Config) error {
	validate := validator.New()
	if err := validate.Struct(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}
