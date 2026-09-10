package fx

import (
	"fmt"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	wildcardOrigin             = string('*')
	defaultAddress             = "127.0.0.1:8081"
	defaultAppName             = "handdraw"
	defaultReadTimeout         = 5 * time.Second
	defaultWriteTimeout        = 5 * time.Second
	defaultIdleTimeout         = 30 * time.Second
	defaultShutdownTimeout     = 10 * time.Second
	defaultToolShutdownTimeout = 10 * time.Second
	defaultProbeTimeout        = 2 * time.Second
)

// MaxRequestIDLength bounds the expected generated correlation identifier in tests.
const MaxRequestIDLength = 128

// Config controls the shared HTTP server behavior.
type Config struct {
	Address             string        `envconfig:"ADDR" default:"127.0.0.1:8081"`
	AppName             string        `split_words:"true" default:"handdraw"`
	ReadTimeout         time.Duration `split_words:"true" default:"5s"`
	WriteTimeout        time.Duration `split_words:"true" default:"5s"`
	IdleTimeout         time.Duration `split_words:"true" default:"30s"`
	ShutdownTimeout     time.Duration `split_words:"true" default:"10s"`
	ToolShutdownTimeout time.Duration `split_words:"true" default:"10s"`
	ProbeTimeout        time.Duration `split_words:"true" default:"2s"`
	BodyLimit           int           `split_words:"true" default:"65536"`
	CORS                CORSConfig
}

// CORSConfig controls browser access to the HTTP server.
type CORSConfig struct {
	Enabled          bool     `default:"false"`
	AllowOrigins     []string `split_words:"true"`
	AllowMethods     []string `split_words:"true"`
	AllowHeaders     []string `split_words:"true"`
	ExposeHeaders    []string `split_words:"true"`
	AllowCredentials bool     `split_words:"true" default:"false"`
	MaxAge           int      `split_words:"true" default:"0"`
}

func (c Config) withDefaults() Config {
	if c.Address == "" {
		c.Address = defaultAddress
	}

	if c.AppName == "" {
		c.AppName = defaultAppName
	}

	if c.ReadTimeout <= 0 {
		c.ReadTimeout = defaultReadTimeout
	}

	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaultWriteTimeout
	}

	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaultIdleTimeout
	}

	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}

	if c.ToolShutdownTimeout <= 0 {
		c.ToolShutdownTimeout = defaultToolShutdownTimeout
	}

	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = defaultProbeTimeout
	}

	if c.BodyLimit <= 0 {
		c.BodyLimit = 65536
	}

	c.CORS = c.CORS.withDefaults()

	return c
}

func (c CORSConfig) withDefaults() CORSConfig {
	if !c.Enabled {
		return c
	}

	if len(c.AllowMethods) == 0 {
		c.AllowMethods = []string{
			fiber.MethodGet,
			fiber.MethodPost,
			fiber.MethodHead,
			fiber.MethodPut,
			fiber.MethodDelete,
			fiber.MethodPatch,
			fiber.MethodOptions,
		}
	}

	if len(c.AllowHeaders) == 0 {
		c.AllowHeaders = []string{
			fiber.HeaderAuthorization,
			fiber.HeaderContentType,
			fiber.HeaderAccept,
			fiber.HeaderIfMatch,
			"Idempotency-Key",
			fiber.HeaderXRequestID,
		}
	}

	c.ExposeHeaders = slices.Clone(c.ExposeHeaders)
	for _, header := range []string{
		fiber.HeaderXRequestID, fiber.HeaderETag,
		"X-Document-Schema-Version", fiber.HeaderLocation, fiber.HeaderRetryAfter,
	} {
		if !slices.Contains(c.ExposeHeaders, header) {
			c.ExposeHeaders = append(c.ExposeHeaders, header)
		}
	}

	return c
}

func (c Config) validate() error {
	if !c.CORS.Enabled {
		return nil
	}

	if len(c.CORS.AllowOrigins) == 0 {
		return fmt.Errorf("fiber: CORS requires at least one allowed origin")
	}

	if c.CORS.AllowCredentials && slices.Contains(c.CORS.AllowOrigins, wildcardOrigin) {
		return fmt.Errorf("fiber: CORS credentials require explicit origins")
	}

	return nil
}
