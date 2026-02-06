package config

import (
	"os"
	"strconv"
	"strings"
)

var lookupEnv = func(key string) (string, bool) { return os.LookupEnv(key) }

func getenvString(key, fallback string) string {
	value, ok := lookupEnvTrimmed(key)
	if !ok {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	value, ok := lookupEnvTrimmed(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value, ok := lookupEnvTrimmed(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func lookupEnvTrimmed(key string) (string, bool) {
	raw, ok := lookupEnv(key)
	if !ok {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	return value, true
}
