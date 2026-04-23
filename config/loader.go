package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Loader loads configuration from various sources.
type Loader struct {
	filePath string
}

// NewLoader creates a new Loader.
func NewLoader(filePath string) *Loader {
	return &Loader{filePath: filePath}
}

// Load reads configuration from file and environment variables.
// File values take precedence over environment variables.
// Defaults are used for any values not set.
func (l *Loader) Load() (*Config, error) {
	cfg := DefaultConfig()

	// Load from environment variables
	if err := l.loadEnv(cfg); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}

	// Load from file (overrides env values)
	if l.filePath != "" {
		if err := l.loadFile(cfg); err != nil {
			return nil, fmt.Errorf("load file %s: %w", l.filePath, err)
		}
	}

	return cfg, nil
}

// loadFile loads configuration from a YAML file.
func (l *Loader) loadFile(cfg *Config) error {
	data, err := os.ReadFile(l.filePath)
	if err != nil {
		return err
	}

	// Detect format by extension
	var unmarshalErr error
	switch {
	case strings.HasSuffix(l.filePath, ".yaml"), strings.HasSuffix(l.filePath, ".yml"):
		unmarshalErr = yaml.Unmarshal(data, cfg)
	case strings.HasSuffix(l.filePath, ".toml"):
		// TOML support requires separate import; for now parse as YAML-like
		unmarshalErr = yaml.Unmarshal(data, cfg)
	default:
		// Try YAML by default
		unmarshalErr = yaml.Unmarshal(data, cfg)
	}

	return unmarshalErr
}

// loadEnv reads environment variables and sets matching config fields.
func (l *Loader) loadEnv(cfg *Config) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		envKey := field.Tag.Get("env")
		if envKey == "" {
			continue
		}

		val, ok := os.LookupEnv(envKey)
		if !ok {
			continue
		}

		fv := v.Field(i)
		if err := setField(fv, val); err != nil {
			return fmt.Errorf("env %s=%s: %w", envKey, val, err)
		}
	}

	return nil
}

// setField sets a struct field value from a string.
func setField(fv reflect.Value, val string) error {
	if !fv.CanSet() {
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(val)
	case reflect.Bool:
		switch strings.ToLower(val) {
		case "true", "1", "yes":
			fv.SetBool(true)
		case "false", "0", "no":
			fv.SetBool(false)
		default:
			return fmt.Errorf("invalid bool value: %s", val)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if fv.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(val)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", val)
			}
			fv.SetInt(int64(d))
		} else {
			var n int64
			if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
				return fmt.Errorf("invalid int value: %s", val)
			}
			fv.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var n uint64
		if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
			return fmt.Errorf("invalid uint value: %s", val)
		}
		fv.SetUint(n)
	default:
		return fmt.Errorf("unsupported field type: %s", fv.Kind())
	}

	return nil
}
