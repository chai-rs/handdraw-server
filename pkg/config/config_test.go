package config_test

import (
	"os"
	"testing"

	configx "github.com/chai-rs/handdraw-server/pkg/config"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/stretchr/testify/require"
)

func TestConfigurationCanBeReadRepeatedlyWithoutChangingEnvironment(t *testing.T) {
	t.Setenv("APP_HTTP_ADDR", "127.0.0.1:9876")
	type settings struct{ HTTP fx.Config }
	before := os.Environ()
	for range 2 {
		conf, err := configx.New[settings]("APP")
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:9876", conf.HTTP.Address)
		require.Equal(t, 65536, conf.HTTP.BodyLimit)
	}
	require.Equal(t, before, os.Environ())
}
