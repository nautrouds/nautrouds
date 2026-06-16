package options

import (
	"flag"
	"os"
	"regexp"
	"strconv"
)

type Options struct {
	ConfigPath      string
	NtucPath        string
	ServicesDir     string
	EntrypointDir   string
	EntrypointCount int
	LogLevel        string
	Token           string
}

func Load() *Options {
	configPtr := EnvString("config", "NAUTROUDS_CONFIG", "nautrouds.ntu", "Path to config file (.ntu or Ntufile)")
	ntucPtr := EnvString("ntuc", "NAUTROUDS_NTUC", "ntuc", "Path to ntuc executable")
	servicesDirPtr := EnvString("services", "NAUTROUDS_SERVICES_DIR", "/var/run/nautrouds/services", "Path to services directory")
	entrypointDirPtr := EnvString("entrypoint-dir", "NAUTROUDS_ENTRYPOINT_DIR", "/var/run/nautrouds/entrypoints", "Path to entrypoint directory")
	entrypointCountPtr := EnvString("entrypoint-count", "NAUTROUDS_ENTRYPOINT_COUNT", "1", "Number of entrypoint instances to spawn")
	logLevelPtr := EnvString("log-level", "NAUTROUDS_LOG_LEVEL", "info", "Log level (debug, info, warn, error, dpanic, panic, fatal)")
	tokenPtr := EnvString("token", "NAUTROUDS_TOKEN", "", "")

	flag.Parse()

	entrypointCount, err := strconv.Atoi(*entrypointCountPtr)
	if err != nil {
		entrypointCount = 1
	}
	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)

	return &Options{
		ConfigPath:      *configPtr,
		NtucPath:        *ntucPtr,
		ServicesDir:     *servicesDirPtr,
		EntrypointDir:   *entrypointDirPtr,
		EntrypointCount: entrypointCount,
		LogLevel:        *logLevelPtr,
		Token:           reg.ReplaceAllString(*tokenPtr, "_"),
	}
}

func EnvString(name, envKey, fallback, usage string) *string {
	return flag.String(name, getEnv(envKey, fallback), usage)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
