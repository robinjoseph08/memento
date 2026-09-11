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
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

const defaultConfigPath = "/config/app.yaml"

type environmentLoadError struct {
	cause error
}

func (e *environmentLoadError) Error() string {
	return "environment: could not load configuration"
}

func (e *environmentLoadError) Unwrap() error {
	return e.cause
}

// Config holds the application configuration. Values can come from a YAML
// file or environment variables. Environment variables take precedence.
type Config struct {
	PublicURL                 string        `koanf:"public_url" json:"public_url" validate:"required"`
	ImmichURL                 string        `koanf:"immich_url" json:"immich_url" validate:"required"`
	ImmichPublicURL           string        `koanf:"immich_public_url" json:"immich_public_url"`
	ImmichAPIKey              string        `koanf:"immich_api_key" json:"-" validate:"required"`
	AuthMode                  string        `koanf:"auth_mode" json:"auth_mode"`
	GoogleClientID            string        `koanf:"google_client_id" json:"google_client_id"`
	GoogleClientSecret        string        `koanf:"google_client_secret" json:"-"`
	AppEnv                    string        `koanf:"app_env" json:"app_env"`
	DatabaseURL               string        `koanf:"database_url" json:"database_url" validate:"required"`
	DatabaseMaxOpenConns      int           `koanf:"database_max_open_conns" json:"database_max_open_conns" validate:"min=1"`
	DatabaseMaxIdleConns      int           `koanf:"database_max_idle_conns" json:"database_max_idle_conns" validate:"min=0,ltefield=DatabaseMaxOpenConns"`
	DatabaseDebug             bool          `koanf:"database_debug" json:"database_debug"`
	DatabaseConnectRetryCount int           `koanf:"database_connect_retry_count" json:"database_connect_retry_count" validate:"min=1"`
	DatabaseConnectRetryDelay time.Duration `koanf:"database_connect_retry_delay" json:"database_connect_retry_delay" validate:"min=0"`
	FilesPath                 string        `koanf:"files_path" json:"files_path" validate:"required"`
	CookieNamespace           string        `koanf:"cookie_namespace" json:"-"`
	ServerHost                string        `koanf:"server_host" json:"server_host" validate:"required"`
	ServerPort                int           `koanf:"server_port" json:"server_port" validate:"min=0,max=65535"`
	Hostname                  string        `koanf:"-" json:"-"`
}

func defaults() *Config {
	return &Config{
		AppEnv:                    "production",
		AuthMode:                  "google",
		DatabaseMaxOpenConns:      10,
		DatabaseMaxIdleConns:      3,
		DatabaseDebug:             false,
		DatabaseConnectRetryCount: 5,
		DatabaseConnectRetryDelay: 2 * time.Second,
		FilesPath:                 "./tmp/files",
		CookieNamespace:           "memento",
		ServerHost:                "0.0.0.0",
		ServerPort:                3579,
	}
}

// ImmichBrowserURL is the Immich origin a Curator's browser can open. It falls
// back to the server-side URL when no separate public URL is configured.
func (c *Config) ImmichBrowserURL() string {
	if c.ImmichPublicURL != "" {
		return c.ImmichPublicURL
	}
	return c.ImmichURL
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
				if os.IsNotExist(fileError) {
					return nil, fmt.Errorf("config_file: %w", fileError)
				}
				return nil, fmt.Errorf("config_file: %w", errorstack.Capture(fileError))
			}
			return nil, fmt.Errorf("config_file: invalid YAML; check syntax and value types")
		}
	}

	if environment != nil {
		if err := k.Load(environment, nil); err != nil {
			return nil, errorstack.Capture(&environmentLoadError{cause: err})
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
		return nil, fmt.Errorf("get hostname: %w", errorstack.Capture(err))
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
		{"immich_public_url", &cfg.ImmichPublicURL},
	} {
		if setting.field == "immich_public_url" && strings.TrimSpace(*setting.value) == "" {
			continue
		}
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
		if strings.HasPrefix(setting.field, "immich") && strings.HasSuffix(strings.ToLower(path.Clean(u.Path)), "/api") {
			return fmt.Errorf("%s: use the instance base URL without /api", setting.field)
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
	if !validCookieNamespace(cfg.CookieNamespace) {
		return fmt.Errorf("cookie_namespace: must be 64 characters or fewer and use lowercase letters, numbers, and underscores")
	}
	switch cfg.AuthMode {
	case "google":
		for _, setting := range []struct{ field, value string }{
			{"google_client_id", cfg.GoogleClientID},
			{"google_client_secret", cfg.GoogleClientSecret},
		} {
			if strings.TrimSpace(setting.value) == "" {
				return fmt.Errorf("%s: required", setting.field)
			}
		}
		public, _ := url.Parse(cfg.PublicURL)
		if public.Scheme != "https" && (public.Hostname() != "localhost" || cfg.AppEnv == "production") {
			return fmt.Errorf("public_url: Google sign-in requires HTTPS, except HTTP localhost in development or test")
		}
	case "fake":
		if cfg.AppEnv != "development" && cfg.AppEnv != "test" {
			return fmt.Errorf("auth_mode: fake requires app_env development or test")
		}
	default:
		return fmt.Errorf("auth_mode: must be google or fake")
	}
	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string { return field.Tag.Get("koanf") })
	if err := validate.Struct(cfg); err != nil {
		var validationErrors validator.ValidationErrors
		if !errors.As(err, &validationErrors) {
			return errorstack.Capture(err)
		}
		field := validationErrors[0]
		return fmt.Errorf("%s: %s %s", field.Field(), field.Tag(), field.Param())
	}
	return nil
}

func validCookieNamespace(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := range len(value) {
		character := value[i]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
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
