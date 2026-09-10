//go:build integration

package testsupport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/asset/infra/s3"
	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// GarageImage pins the real S3 implementation exercised by all storage integration tests.
const GarageImage = "dxflrs/garage:v2.3.0@sha256:866bd13ed2038ba7e7190e840482bc27234c4afaf77be8cfa439ae088c1e4690"

// Garage starts an isolated single-node store with generated credentials and a private bucket.
func Garage(t *testing.T) s3.Config {
	t.Helper()

	secret := func(n int) string {
		b := make([]byte, n)
		_, err := rand.Read(b)
		require.NoError(t, err)

		return hex.EncodeToString(b)
	}
	config := s3.Config{Region: "garage", Bucket: "handdraw-test", AccessKey: "GK" + secret(16), SecretKey: secret(32)}
	toml := fmt.Sprintf(`metadata_dir = "/tmp/meta"
data_dir = "/tmp/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "0.0.0.0:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = %q
[s3_api]
s3_region = "garage"
api_bind_addr = "0.0.0.0:3900"
`, secret(32))

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	instance, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{Image: GarageImage, Cmd: []string{"/garage", "server", "--single-node", "--default-bucket"}, ExposedPorts: []string{"3900/tcp"}, Env: map[string]string{"GARAGE_DEFAULT_ACCESS_KEY": config.AccessKey, "GARAGE_DEFAULT_SECRET_KEY": config.SecretKey, "GARAGE_DEFAULT_BUCKET": config.Bucket, "RUST_LOG": "warn"}, Files: []testcontainers.ContainerFile{{Reader: bytes.NewReader([]byte(toml)), ContainerFilePath: "/etc/garage.toml", FileMode: 0o600}}, WaitingFor: wait.ForListeningPort("3900/tcp").SkipInternalCheck(), HostConfigModifier: func(c *container.HostConfig) {
		for port, bindings := range c.PortBindings {
			for i := range bindings {
				bindings[i].HostIP = netip.MustParseAddr("127.0.0.1")
			}

			c.PortBindings[port] = bindings
		}
	}}, Started: true})
	if instance != nil {
		testcontainers.CleanupContainer(t, instance)
	}

	require.NoError(t, err)
	host, err := instance.Host(ctx)
	require.NoError(t, err)
	port, err := instance.MappedPort(ctx, "3900/tcp")
	require.NoError(t, err)

	config.Endpoint = fmt.Sprintf("http://%s:%s", host, port.Port())
	storage, err := s3.New(config)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return storage.Check(ctx) == nil }, 20*time.Second, 100*time.Millisecond)

	return config
}
