package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"reflect"
	"strconv"
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
	PublicURL                 string        `koanf:"public_url" json:"public_url" validate:"required"`
	ImmichURL                 string        `koanf:"immich_url" json:"immich_url" validate:"required"`
	ImmichAPIKey              string        `koanf:"immich_api_key" json:"-" validate:"required"`
	AuthMode                  string        `koanf:"auth_mode" json:"auth_mode" validate:"required"`
	AppEnv                    string        `koanf:"app_env" json:"app_env"`
	DatabaseURL               string        `koanf:"database_url" json:"database_url" validate:"required"`
	DatabaseMaxOpenConns      int           `koanf:"database_max_open_conns" json:"database_max_open_conns" validate:"min=1"`
	DatabaseMaxIdleConns      int           `koanf:"database_max_idle_conns" json:"database_max_idle_conns" validate:"min=0,ltefield=DatabaseMaxOpenConns"`
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
		AppEnv:                    "production",
		DatabaseMaxOpenConns:      10,
		DatabaseMaxIdleConns:      3,
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
			if fileError, ok := errors.AsType[*os.PathError](err); ok {
				return nil, fmt.Errorf("config_file: %w", fileError)
			}
			return nil, fmt.Errorf("config_file: invalid YAML; check syntax and value types")
		}
	}

	if environment != nil {
		if err := k.Load(environment, nil); err != nil {
			return nil, fmt.Errorf("environment: could not load configuration")
		}
	}

	// Decode one setting at a time so errors identify the field without
	// exposing values from the decoder's error message.
	fields := reflect.ValueOf(cfg).Elem()
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Type().Field(i).Tag.Get("koanf")
		if field == "-" || !k.Exists(field) {
			continue
		}
		if fields.Field(i).Kind() == reflect.String {
			if _, ok := k.Get(field).(string); !ok {
				return nil, fmt.Errorf("%s: must be a string", field)
			}
		}
		if err := k.Unmarshal(field, fields.Field(i).Addr().Interface()); err != nil {
			return nil, fmt.Errorf("%s: invalid value", field)
		}
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
	cfg.PublicURL = "http://localhost:3579"
	cfg.ImmichURL = "http://localhost:2283"
	cfg.ImmichAPIKey = "test-api-key"
	cfg.AuthMode = "fake"
	cfg.AppEnv = "test"
	cfg.DatabaseMaxOpenConns = 3
	cfg.DatabaseMaxIdleConns = 1
	cfg.DatabaseConnectRetryCount = 1
	cfg.DatabaseConnectRetryDelay = 0
	cfg.DatabaseDebug = true
	cfg.ServerHost = "127.0.0.1"
	cfg.ServerPort = 0
	cfg.Hostname = "test-host"
	return cfg
}

func validateConfig(cfg *Config) error {
	for _, setting := range []struct{ field, value string }{
		{"database_url", cfg.DatabaseURL},
		{"public_url", cfg.PublicURL},
		{"immich_url", cfg.ImmichURL},
		{"immich_api_key", cfg.ImmichAPIKey},
		{"auth_mode", cfg.AuthMode},
	} {
		if strings.TrimSpace(setting.value) == "" {
			return fmt.Errorf("%s: required", setting.field)
		}
	}
	db, err := parseURL("database_url", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if db.Scheme != "postgres" && db.Scheme != "postgresql" {
		return fmt.Errorf("database_url: must use postgres or postgresql")
	}
	if db.User == nil || strings.TrimSpace(db.User.Username()) == "" {
		return fmt.Errorf("database_url: must include an explicit database role")
	}
	if strings.TrimSpace(strings.TrimPrefix(db.Path, "/")) == "" || strings.Contains(strings.TrimPrefix(db.Path, "/"), "/") {
		return fmt.Errorf("database_url: must include one database name")
	}
	if strings.Contains(cfg.DatabaseURL, "#") {
		return fmt.Errorf("database_url: must not contain a fragment")
	}
	if _, err := url.ParseQuery(db.RawQuery); err != nil {
		return fmt.Errorf("database_url: invalid query parameters")
	}
	for _, setting := range []struct {
		field string
		value *string
	}{
		{"public_url", &cfg.PublicURL},
		{"immich_url", &cfg.ImmichURL},
	} {
		u, err := parseURL(setting.field, *setting.value)
		if err != nil {
			return err
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%s: must use http or https", setting.field)
		}
		if u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(*setting.value, "#") {
			return fmt.Errorf("%s: must not contain credentials, a query, or a fragment", setting.field)
		}
		if setting.field == "public_url" && u.Path != "" && u.Path != "/" {
			return fmt.Errorf("public_url: must be an origin without a path")
		}
		if setting.field == "immich_url" && strings.HasSuffix(strings.ToLower(path.Clean(u.Path)), "/api") {
			return fmt.Errorf("immich_url: use the instance base URL without /api")
		}
		if setting.field == "public_url" {
			u.Host = strings.ToLower(u.Host)
			if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
				host := u.Hostname()
				if strings.Contains(host, ":") {
					host = "[" + host + "]"
				}
				u.Host = host
			}
		}
		*setting.value = strings.TrimRight(u.String(), "/")
	}
	if cfg.AppEnv != "production" && cfg.AppEnv != "development" && cfg.AppEnv != "test" {
		return fmt.Errorf("app_env: must be production, development, or test")
	}
	switch cfg.AuthMode {
	case "google":
		return fmt.Errorf("auth_mode: google is not available yet; Google authentication arrives in #7")
	case "fake":
		if cfg.AppEnv != "development" && cfg.AppEnv != "test" {
			return fmt.Errorf("auth_mode: fake requires app_env development or test")
		}
	default:
		return fmt.Errorf("auth_mode: must be fake; Google authentication is not available yet")
	}
	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string { return field.Tag.Get("koanf") })
	if err := validate.Struct(cfg); err != nil {
		var validationErrors validator.ValidationErrors
		if !errors.As(err, &validationErrors) {
			return err
		}
		field := validationErrors[0]
		return fmt.Errorf("%s: %s %s", field.Field(), field.Tag(), field.Param())
	}
	return nil
}

// parseURL reports the field, never the input, because URLs can contain secrets.
func parseURL(field, value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid URL", field)
	}
	if u.Hostname() == "" || u.Opaque != "" {
		return nil, fmt.Errorf("%s: must include a host", field)
	}
	if (strings.Contains(u.Hostname(), ":") || strings.HasPrefix(u.Host, "[")) && net.ParseIP(u.Hostname()) == nil {
		return nil, fmt.Errorf("%s: invalid host", field)
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("%s: invalid port", field)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%s: port must be between 1 and 65535", field)
		}
	}
	return u, nil
}
