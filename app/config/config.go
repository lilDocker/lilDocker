package config

import (
	"os"
	"path/filepath"
)

func Home() string {
	if v := os.Getenv("LILDOCKER_HOME"); v != "" {
		return v
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".lildocker"
	}
	return filepath.Join(h, ".lildocker")
}

func Dir(sub string) string {
	return filepath.Join(Home(), sub)
}
