package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Output          *string `yaml:"output"`
	TokenFile       *string `yaml:"token-file"`
	NodePath        *string `yaml:"node-path"`
	GranolaPath     *string `yaml:"granola-path"`
	Days            *int    `yaml:"days"`
	Since           *string `yaml:"since"`
	SinceLastExport *bool   `yaml:"since-last-export"`
	Overwrite       *bool   `yaml:"overwrite"`
	Debug           *bool   `yaml:"debug"`
	Exclude         *string `yaml:"exclude"`
}

func FindPath() string {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-config" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], "-config=") {
			return strings.TrimPrefix(args[i], "-config=")
		}
	}
	for _, p := range []string{"granola-backup.yaml", "granola-backup.yml"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func Load(path string) *Config {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func StrVal(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func IntVal(i *int, def int) int {
	if i != nil {
		return *i
	}
	return def
}

func BoolVal(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}
