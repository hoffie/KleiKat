package main

import (
	"gopkg.in/yaml.v3"
	"os"
)

type Config struct {
	Tokens struct {
		Read      string `yaml:"read"`
		ReadWrite string `yaml:"read_write"`
	} `yaml:"tokens"`
	Schemas map[string]Scheme `yaml:"schemas"`
}

type Scheme struct {
	Title           string            `yaml:"title"`
	AttributeTitles [][]string `yaml:"attribute_titles"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}

	// Load schemas from schemas.yaml if not already loaded
	if cfg.Schemas == nil {
		schemasData, err := os.ReadFile("schemas.yaml")
		if err == nil {
			err = yaml.Unmarshal(schemasData, &cfg.Schemas)
			if err != nil {
				return nil, err
			}
		}
	}

	return &cfg, nil
}
