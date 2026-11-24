package testkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Database servers are found, in order, from an engine-specific variable
// (TEST_POSTGRES_URL, TEST_MYSQL_URL, TEST_MONGO_URL), from
// TEST_DATABASE_URL when its scheme matches the engine (CI service
// containers), or by starting one container per test process with
// testcontainers. Without Docker the tests are skipped.
const EnvDatabaseURL = "TEST_DATABASE_URL"

type engine struct {
	name    string
	envVar  string
	schemes []string
	start   func(ctx context.Context) (testcontainers.Container, string, error)
}

var engines = map[string]engine{
	"postgres": {"postgres", "TEST_POSTGRES_URL", []string{"postgres://", "postgresql://"}, startPostgres},
	"mysql":    {"mysql", "TEST_MYSQL_URL", []string{"mysql://"}, startMySQL},
	"mongo":    {"mongo", "TEST_MONGO_URL", []string{"mongodb://", "mongodb+srv://"}, startMongo},
}

// Process-wide server registry. Containers are expensive, so each test
// process starts at most one per engine and Main stops it. This shared
// state is test-only.
var (
	serversMu sync.Mutex
	servers   = map[string]*server{}
)

type server struct {
	url       string
	container testcontainers.Container
	err       error
}

// serverURL returns the admin URL of a running server for engine, starting
// a container on first use. It skips the test when none is available.
func serverURL(t testing.TB, name string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("testkit: skipping database test in -short mode")
	}
	e := engines[name]
	if u := os.Getenv(e.envVar); u != "" {
		return u
	}
	if u := os.Getenv(EnvDatabaseURL); u != "" {
		for _, s := range e.schemes {
			if strings.HasPrefix(u, s) {
				return u
			}
		}
	}
	serversMu.Lock()
	defer serversMu.Unlock()
	s, ok := servers[name]
	if !ok {
		s = &server{}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		s.container, s.url, s.err = e.start(ctx)
		cancel()
		servers[name] = s
	}
	if s.err != nil {
		t.Skipf("testkit: no %s server: set %s or %s, or make Docker available (%v)", name, e.envVar, EnvDatabaseURL, s.err)
	}
	return s.url
}

// stopServers terminates the containers this process started.
func stopServers() {
	serversMu.Lock()
	defer serversMu.Unlock()
	for name, s := range servers {
		if s.container != nil {
			_ = s.container.Terminate(context.Background())
		}
		delete(servers, name)
	}
}

func run(ctx context.Context, req testcontainers.ContainerRequest) (testcontainers.Container, string, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return nil, "", err
	}
	addr, err := c.Endpoint(ctx, "") // host:port of the first exposed port
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, "", err
	}
	return c, addr, nil
}

func startPostgres(ctx context.Context) (testcontainers.Container, string, error) {
	c, addr, err := run(ctx, testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env:          map[string]string{"POSTGRES_PASSWORD": "semii"},
		WaitingFor:   wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2 * time.Minute),
	})
	return c, "postgres://postgres:semii@" + addr + "/postgres?sslmode=disable", err
}

func startMySQL(ctx context.Context) (testcontainers.Container, string, error) {
	c, addr, err := run(ctx, testcontainers.ContainerRequest{
		Image:        "mysql:8.4",
		ExposedPorts: []string{"3306/tcp"},
		Env:          map[string]string{"MYSQL_ROOT_PASSWORD": "semii"},
		WaitingFor:   wait.ForLog("port: 3306  MySQL Community Server").WithStartupTimeout(3 * time.Minute),
	})
	return c, "mysql://root:semii@" + addr + "/mysql", err
}

// startMongo runs a single-node replica set, so transactions work.
func startMongo(ctx context.Context) (testcontainers.Container, string, error) {
	c, addr, err := run(ctx, testcontainers.ContainerRequest{
		Image:        "mongo:7",
		ExposedPorts: []string{"27017/tcp"},
		Cmd:          []string{"mongod", "--replSet", "rs0", "--bind_ip_all"},
		WaitingFor:   wait.ForLog("Waiting for connections").WithStartupTimeout(2 * time.Minute),
	})
	if err != nil {
		return nil, "", err
	}
	code, out, err := c.Exec(ctx, []string{"mongosh", "--quiet", "--eval",
		`try { rs.status().ok } catch (e) { rs.initiate({_id: "rs0", members: [{_id: 0, host: "localhost:27017"}]}).ok }`})
	if err == nil && code != 0 {
		b, _ := io.ReadAll(out)
		err = fmt.Errorf("rs.initiate: exit %d: %s", code, b)
	}
	// rs.initiate returns before the election; wait until writes work.
	for err == nil {
		code, out, err = c.Exec(ctx, []string{"mongosh", "--quiet", "--eval", "db.hello().isWritablePrimary"})
		if err != nil {
			break
		}
		b, _ := io.ReadAll(out)
		if code == 0 && strings.Contains(string(b), "true") {
			break
		}
		select {
		case <-ctx.Done():
			err = fmt.Errorf("replica set never became primary: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, "", err
	}
	return c, "mongodb://" + addr + "/?directConnection=true", nil
}
