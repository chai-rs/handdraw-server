package fx

import (
	"fmt"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	recovermw "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

func registerMiddleware(app *fiber.App, config Config) error {
	app.Use(replaceIncomingRequestID)
	app.Use(requestid.New())
	app.Use(func(c fiber.Ctx) error { c.Set(fiber.HeaderCacheControl, "private, no-store"); return c.Next() })
	app.Use(requestContext)
	app.Use(recovermw.New())

	if !config.CORS.Enabled {
		return nil
	}

	middleware, err := newCORSMiddleware(config.CORS)
	if err != nil {
		return err
	}

	app.Use(middleware)

	return nil
}

func newCORSMiddleware(config CORSConfig) (handler fiber.Handler, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			handler = nil
			err = fmt.Errorf("fiber: invalid CORS configuration: %v", recovered)
		}
	}()

	return cors.New(cors.Config{
		AllowOrigins:     config.AllowOrigins,
		AllowMethods:     config.AllowMethods,
		AllowHeaders:     config.AllowHeaders,
		ExposeHeaders:    config.ExposeHeaders,
		AllowCredentials: config.AllowCredentials,
		MaxAge:           config.MaxAge,
	}), nil
}

func replaceIncomingRequestID(c fiber.Ctx) error {
	c.Request().Header.Del(fiber.HeaderXRequestID)

	return c.Next()
}

func requestContext(c fiber.Ctx) error {
	requestID := requestid.FromContext(c)
	ctx := errx.WithBuilder(c.Context(), errx.RequestID(requestID))
	c.SetContext(ctx)

	if err := c.Next(); err != nil {
		if _, ok := errx.AsError(err); ok {
			return errx.FromContext(c.Context()).Wrap(err)
		}

		return err
	}

	return nil
}
