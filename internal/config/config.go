package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Server ServerConfig
	Auth   AuthConfig
	Queue  QueueConfig
	Result ResultConfig
	Log    LogConfig
}

type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type AuthConfig struct {
	OnlineAPIKeys []string
	LocalAPIKeys  []string
	AdminAPIKey   string // optional; if empty, /metrics is unauthenticated
}

type QueueConfig struct {
	MaxSize      int
	RequestTTL   time.Duration
	MaxBatchSize int
}

type ResultConfig struct {
	LongPollTimeout time.Duration
	ResultTTL       time.Duration
}

type LogConfig struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

// Load reads configuration from environment variables.
// Missing required keys cause an error; missing optional keys use defaults.
func Load() (*Config, error) {
	onlineKeys, err := requireEnvList("HR_ONLINE_API_KEYS")
	if err != nil {
		return nil, err
	}
	localKeys, err := requireEnvList("HR_LOCAL_API_KEYS")
	if err != nil {
		return nil, err
	}

	port := envInt("HR_PORT", 8080)

	writeTimeout := envDuration("HR_WRITE_TIMEOUT", 35*time.Second)
	longPollTimeout := envDuration("HR_LONGPOLL_TIMEOUT", 30*time.Second)

	if writeTimeout <= longPollTimeout {
		return nil, fmt.Errorf(
			"HR_WRITE_TIMEOUT (%s) must be greater than HR_LONGPOLL_TIMEOUT (%s)",
			writeTimeout, longPollTimeout,
		)
	}

	cfg := &Config{
		Server: ServerConfig{
			Host:            envString("HR_HOST", "0.0.0.0"),
			Port:            port,
			ReadTimeout:     envDuration("HR_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:    writeTimeout,
			ShutdownTimeout: envDuration("HR_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Auth: AuthConfig{
			OnlineAPIKeys: onlineKeys,
			LocalAPIKeys:  localKeys,
			AdminAPIKey:   os.Getenv("HR_ADMIN_API_KEY"),
		},
		Queue: QueueConfig{
			MaxSize:      envInt("HR_QUEUE_MAX_SIZE", 10000),
			RequestTTL:   envDuration("HR_REQUEST_TTL", 5*time.Minute),
			MaxBatchSize: envInt("HR_MAX_BATCH_SIZE", 100),
		},
		Result: ResultConfig{
			LongPollTimeout: longPollTimeout,
			ResultTTL:       envDuration("HR_RESULT_TTL", 10*time.Minute),
		},
		Log: LogConfig{
			Level:  envString("HR_LOG_LEVEL", "info"),
			Format: envString("HR_LOG_FORMAT", "json"),
		},
	}

	return cfg, nil
}

// Addr returns the "host:port" listen address.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func requireEnvList(key string) ([]string, error) {
	val := os.Getenv(key)
	if val == "" {
		return nil, fmt.Errorf("required environment variable %s is not set", key)
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("required environment variable %s has no valid keys", key)
	}
	return result, nil
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
