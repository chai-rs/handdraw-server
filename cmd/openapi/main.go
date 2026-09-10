// Command openapi serves Handdraw's reviewed API contract through Scalar independently of the API runtime.
package main

import (
	"context"
	_ "embed"
	"os"
	"os/signal"
	"syscall"

	"github.com/chai-rs/handdraw-server/contracts"
	configx "github.com/chai-rs/handdraw-server/pkg/config"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	logx "github.com/chai-rs/handdraw-server/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

//go:embed index.html
var index string

type configuration struct {
	Address string `envconfig:"ADDR" default:"127.0.0.1:8082"`
}

func main() {
	conf, err := configx.New[configuration]("OPENAPI")
	if err != nil {
		logx.Error().Msg("invalid documentation configuration")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := fx.New(fx.Params{Config: fx.Config{Address: conf.Address, AppName: "handdraw-openapi"}, Routes: routes})
	if err != nil {
		logx.Error().Err(err).Msg("documentation setup failed")
		os.Exit(1)
	}

	logx.Info().Str("address", conf.Address).Msg("Handdraw Scalar reference starting")

	if err = server.Run(ctx); err != nil {
		logx.Error().Err(err).Msg("documentation server stopped")
		os.Exit(1)
	}
}

func routes(router fiber.Router) {
	router.Get("/", func(c fiber.Ctx) error { return c.Type("html").SendString(index) })
	router.Get("/openapi.json", func(c fiber.Ctx) error { return c.Type("json").Send(contracts.OpenAPI) })
}
