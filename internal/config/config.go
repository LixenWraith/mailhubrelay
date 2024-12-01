package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LixenWraith/logger"
	"github.com/LixenWraith/tinytoml"
)

const (
	defaultConfigBase = "/usr/local/etc"
)

type SMTPConfig struct {
	Host     string `toml:"host"`
	Port     string `toml:"port"`
	FromAddr string `toml:"from_addr"`
	AuthUser string `toml:"auth_user"`
	AuthPass string `toml:"auth_pass"`
}

type ServerConfig struct {
	InternalAddr   string        `toml:"internal_addr"`
	ExternalAddr   string        `toml:"external_addr"`
	Timeout        time.Duration `toml:"timeout"`
	RetryDelay     time.Duration `toml:"retry_delay"`
	MaxRetries     int           `toml:"max_retries"`
	AllowedOrigins []string      `toml:"allowed_origins"`
}

type Config struct {
	SMTP    SMTPConfig    `toml:"smtp"`
	Server  ServerConfig  `toml:"server"`
	Logging logger.Config `toml:"logging"`
}

var defaultConfig = Config{
	SMTP: SMTPConfig{
		Host:     "smtp.gmail.com",
		Port:     "587",
		FromAddr: "user@example.com",
		AuthUser: "user@example.com",
		AuthPass: "0123456789AB",
	},
	Server: ServerConfig{
		InternalAddr:   "localhost:2525",
		ExternalAddr:   "localhost:8845",
		Timeout:        3 * time.Minute,
		RetryDelay:     10 * time.Second,
		MaxRetries:     3,
		AllowedOrigins: []string{"https://example.com", "http://example.com"},
	},
	Logging: logger.Config{
		Level:          logger.LevelDebug,
		Name:           "",
		Directory:      "/var/log",
		BufferSize:     1000,
		MaxSizeMB:      100,
		MaxTotalSizeMB: 1000,
		MinDiskFreeMB:  500,
	},
}

func Load(name string) (*Config, bool, error) {
	defaultConfigPath := filepath.Join(defaultConfigBase, name, name+".toml")

	if err := os.MkdirAll(filepath.Dir(defaultConfigPath), 0755); err != nil {
		return nil, false, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Start with default config
	config := defaultConfig
	config.Logging.Name = name
	config.Logging.Directory = filepath.Join(config.Logging.Directory, name)

	// If config file exists, Load and merge with defaults
	configExists := false
	if _, err := os.Stat(defaultConfigPath); err == nil {
		configExists = true
		data, err := os.ReadFile(defaultConfigPath)
		if err != nil {
			return nil, configExists, fmt.Errorf("failed to read config file: %w", err)
		}

		// Unmarshal into config, overwriting only specified values
		if err := tinytoml.Unmarshal(data, &config); err != nil {
			return nil, configExists, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	if err := validateConfig(&config); err != nil {
		return nil, configExists, err
	}

	return &config, configExists, nil
}

func validateConfig(config *Config) error {
	// Basic validation
	if config.SMTP.Host == "" || config.SMTP.Port == "" ||
		config.SMTP.FromAddr == "" || config.SMTP.AuthUser == "" ||
		config.SMTP.AuthPass == "" {
		return fmt.Errorf("missing required SMTP configuration")
	}

	if config.Server.InternalAddr == "" || config.Server.Timeout <= 0 ||
		config.Server.RetryDelay <= 0 || config.Server.MaxRetries <= 0 {
		return fmt.Errorf("invalid internal server configuration")
	}

	if config.Logging.Directory == "" || config.Logging.BufferSize <= 0 {
		return fmt.Errorf("invalid logging configuration")
	}

	return nil
}

func Save(config *Config, name string) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	defaultConfigPath := filepath.Join(defaultConfigBase, name, name+".toml")

	data, err := tinytoml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(defaultConfigPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
