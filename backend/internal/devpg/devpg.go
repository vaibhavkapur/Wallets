// Package devpg starts an embedded PostgreSQL for development and tests.
// It is never used in production; the server expects DATABASE_URL.
package devpg

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Instance is a running embedded server.
type Instance struct {
	pg  *embeddedpostgres.EmbeddedPostgres
	DSN string
	dir string
}

// Start launches PostgreSQL 16 on a free port with data in a temp directory.
// Binaries are cached under the OS temp dir between runs.
func Start() (*Instance, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "mywallet-pg-*")
	if err != nil {
		return nil, err
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(uint32(port)).
		Username("postgres").Password("postgres").Database("mywallet").
		RuntimePath(filepath.Join(dir, "runtime")).
		DataPath(filepath.Join(dir, "data")).
		CachePath(filepath.Join(os.TempDir(), "mywallet-embedded-postgres-cache")).
		Logger(nil)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start embedded postgres: %w", err)
	}
	return &Instance{pg: pg, dir: dir, DSN: fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/mywallet?sslmode=disable", port)}, nil
}

// Stop shuts the server down and removes its data directory.
func (i *Instance) Stop() {
	_ = i.pg.Stop()
	os.RemoveAll(i.dir)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
