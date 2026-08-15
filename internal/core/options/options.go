package options

import (
	"flag"
	"fmt"
	"nautrouds/internal/version"
	"os"
	"regexp"
	"strconv"
)

type Options struct {
	ConfigPath        string
	NtucPath          string
	ServicesDir       string
	ServicesDirMode   os.FileMode
	EntrypointDir     string
	EntrypointDirMode os.FileMode
	EntrypointCount   int
	LogLevel          string
	InstanceID        string
	MetricsPath       string
	MetricsSockMode   os.FileMode
	DefaultWelcome    bool
}

func Load() *Options {
	configPtr := EnvString("config", "NAUTROUDS_CONFIG", "nautrouds.ntu", "Path to config file (.ntu or Ntufile)")
	ntucPtr := EnvString("ntuc", "NAUTROUDS_NTUC", "ntuc", "Path to ntuc executable")
	servicesDirPtr := EnvString("services", "NAUTROUDS_SERVICES_DIR", "/var/run/nautrouds/services", "Path to services directory")
	servicesDirModePtr := EnvString("services-dir-mode", "NAUTROUDS_SERVICES_DIR_MODE", "1777", "Permission mode for the services directory (octal, e.g. 0770)")
	entrypointDirPtr := EnvString("entrypoint-dir", "NAUTROUDS_ENTRYPOINT_DIR", "/var/run/nautrouds/entrypoints", "Path to entrypoint directory")
	entrypointDirModePtr := EnvString("entrypoint-dir-mode", "NAUTROUDS_ENTRYPOINT_DIR_MODE", "0755", "Permission mode for the entrypoint directory (octal)")
	entrypointCountPtr := EnvString("entrypoint-count", "NAUTROUDS_ENTRYPOINT_COUNT", "1", "Number of entrypoint instances to spawn")
	logLevelPtr := EnvString("log-level", "NAUTROUDS_LOG_LEVEL", "info", "Log level (debug, info, warn, error, dpanic, panic, fatal)")
	instanceIDPtr := EnvString("instance-id", "NAUTROUDS_INSTANCE_ID", "", "Identifier appended to entrypoint socket names to distinguish this instance")
	metricsPathPtr := EnvString("metrics-socket", "NAUTROUDS_METRICS_SOCKET", "", "Metrics collector socket path (relative to services dir)")
	metricsSockModePtr := EnvString("metrics-socket-mode", "NAUTROUDS_METRICS_SOCKET_MODE", "0666", "Permission mode for the metrics collector socket (octal, e.g. 0660)")
	defaultWelcomePtr := EnvBool("default-welcome", "NAUTROUDS_DEFAULT_WELCOME", true, "Serve a built-in welcome page for all requests when the config file is missing, instead of failing to start")
	showVersionPtr := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *showVersionPtr {
		fmt.Println(version.Get())
		os.Exit(0)
	}

	entrypointCount, err := strconv.Atoi(*entrypointCountPtr)
	if err != nil {
		entrypointCount = 1
	}

	servicesDirMode := mustParseFileMode("services-dir-mode", *servicesDirModePtr)
	entrypointDirMode := mustParseFileMode("entrypoint-dir-mode", *entrypointDirModePtr)
	metricsSockMode := mustParseFileMode("metrics-socket-mode", *metricsSockModePtr)

	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)

	return &Options{
		ConfigPath:        *configPtr,
		NtucPath:          *ntucPtr,
		ServicesDir:       *servicesDirPtr,
		ServicesDirMode:   servicesDirMode,
		EntrypointDir:     *entrypointDirPtr,
		EntrypointDirMode: entrypointDirMode,
		EntrypointCount:   entrypointCount,
		LogLevel:          *logLevelPtr,
		InstanceID:        reg.ReplaceAllString(*instanceIDPtr, "_"),
		MetricsPath:       *metricsPathPtr,
		MetricsSockMode:   metricsSockMode,
		DefaultWelcome:    *defaultWelcomePtr,
	}
}

func mustParseFileMode(flagName, s string) os.FileMode {
	mode, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --%s %q: %v\n", flagName, s, err)
		os.Exit(1)
	}
	return os.FileMode(mode)
}

func EnvString(name, envKey, fallback, usage string) *string {
	return flag.String(name, getEnv(envKey, fallback), usage)
}

func EnvBool(name, envKey string, fallback bool, usage string) *bool {
	value := fallback
	if s, ok := os.LookupEnv(envKey); ok {
		if parsed, err := strconv.ParseBool(s); err == nil {
			value = parsed
		}
	}
	return flag.Bool(name, value, usage)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
