// Package bunx provides PostgreSQL connections, query helpers, and RLS-safe transaction adapters.
package bunx

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// PGConfig contains a connection URL and bounded pool settings, separate from migration credentials.
type PGConfig struct {
	URL             string        `json:"-" required:"true"`
	MaxOpenConns    int           `json:"max_open_conns" split_words:"true" default:"10"`
	MaxIdleConns    int           `json:"max_idle_conns" split_words:"true" default:"5"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime" split_words:"true" default:"5m"`
	ReadTimeout     time.Duration `json:"read_timeout" split_words:"true" default:"30s"`
	PingTimeout     time.Duration `json:"ping_timeout" split_words:"true" default:"5s"`
}

// New opens the configured pool, verifies connectivity within a deadline, and closes it on failure.
// Remote URLs must explicitly request TLS. SQL query logging is intentionally not enabled.
func (c PGConfig) New(ctx context.Context) (*bun.DB, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}

	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if !local {
		switch u.Query().Get("sslmode") {
		case "require", "verify-ca", "verify-full":
		default:
			return nil, errors.New("remote PostgreSQL connections require explicit TLS")
		}
	}

	if c.MaxOpenConns < 0 || c.MaxIdleConns < 0 || c.ConnMaxLifetime < 0 || c.ReadTimeout < 0 || c.PingTimeout < 0 {
		return nil, errors.New("invalid PostgreSQL pool limits")
	}

	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 10
	}

	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 5
	}

	if c.MaxIdleConns > c.MaxOpenConns {
		c.MaxIdleConns = c.MaxOpenConns
	}

	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = 5 * time.Minute
	}

	if c.ReadTimeout == 0 {
		c.ReadTimeout = 30 * time.Second
	}

	if c.PingTimeout == 0 {
		c.PingTimeout = 5 * time.Second
	}

	connector, err := c.connector()
	if err != nil {
		return nil, err
	}

	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(c.MaxOpenConns)
	pool.SetMaxIdleConns(c.MaxIdleConns)
	pool.SetConnMaxLifetime(c.ConnMaxLifetime)
	db := bun.NewDB(pool, pgdialect.New())

	pingCtx, cancel := context.WithTimeout(ctx, c.PingTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, errors.New("PostgreSQL connection check failed")
	}

	return db, nil
}

// The driver panics on malformed DSN options; configuration errors must not expose the URL.
func (c PGConfig) connector() (connector *pgdriver.Connector, err error) {
	defer func() {
		if recover() != nil {
			connector = nil
			err = errors.New("invalid PostgreSQL connection options")
		}
	}()

	return pgdriver.NewConnector(pgdriver.WithDSN(c.URL), pgdriver.WithReadTimeout(c.ReadTimeout)), nil
}
