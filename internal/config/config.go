package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/Tethik/massrepo/internal/workspace"
)

// Config holds the application configuration loaded from disk.
type Config struct {
	RepoPath       string                         `mapstructure:"repo_path"`
	DataPath       string                         `mapstructure:"data_path"`
	BackstageURL   string                         `mapstructure:"backstage_url"`
	BackstageToken string                         `mapstructure:"backstage_token"`
	Groups         map[string]map[string][]string `mapstructure:"groups"`
	// Skills are seeded into every new workspace's .claude/skills directory.
	Skills []workspace.SkillSource `mapstructure:"skills"`
	// MCPServers is a library of named MCP server definitions.
	MCPServers map[string]workspace.MCPServer `mapstructure:"mcp_servers"`
	// DefaultMCPServers names the servers from MCPServers enabled by default in
	// every new workspace.
	DefaultMCPServers []string `mapstructure:"default_mcp_servers"`
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
	if err := v.Unmarshal(&cfg, viper.DecodeHook(decodeHook())); err != nil {
		return nil, fmt.Errorf("parse config: %v", err)
	}

	return &cfg, nil
}

// decodeHook composes viper's default hooks with skillSourceHook so a skill
// entry may be written as a bare string (a local path) or a mapping.
func decodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		skillSourceHook(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)
}

// skillSourceHook decodes a scalar string skill entry into a SkillSource with
// its Path set, leaving mapping entries to the default decoder.
func skillSourceHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeFor[workspace.SkillSource]() || from.Kind() != reflect.String {
			return data, nil
		}
		return workspace.SkillSource{Path: data.(string)}, nil
	}
}
