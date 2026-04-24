package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the application configuration loaded from disk.
type Config struct {
	RepoPath       string                         `mapstructure:"repo_path"`
	DataPath       string                         `mapstructure:"data_path"`
	BackstageURL   string                         `mapstructure:"backstage_url"`
	BackstageToken string                         `mapstructure:"backstage_token"`
	Groups         map[string]map[string][]string `mapstructure:"groups"`
}

// Load reads ~/.config/massrepo/config.yaml, creating it with defaults if absent.
func Load() (*Config, error) {
	home, ok := os.LookupEnv("HOME")
	if !ok {
		return nil, fmt.Errorf("HOME environment variable not set")
	}

	configDir := filepath.Join(home, ".config", "massrepo")
	configPath := filepath.Join(configDir, "config.yaml")

	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.SetDefault("repo_path", filepath.Join(home, "repos"))
	v.SetDefault("data_path", filepath.Join(home, ".massrepo"))

	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return nil, fmt.Errorf("create config directory: %v", err)
	}

	if err := v.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %v", err)
		}
		if err := v.WriteConfigAs(configPath); err != nil {
			return nil, fmt.Errorf("write default config: %v", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %v", err)
	}

	return &cfg, nil
}
