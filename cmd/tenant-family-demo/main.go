// Command tenant-family-demo is the demo's multi-tenant binary. It serves the
// partnerportal module, which shows the HTTP tenant features go-bricks offers only
// with multitenant enabled: a tenant-owned route family whose tenant comes from a
// JWT claim (ADR-164), and the tenant seat's header and path resolution with the
// path-provenance existence check (ADR-170).
//
// cmd/api stays single-tenant. This binary loads its own standalone config,
// cmd/tenant-family-demo/config.yaml, and boots with the same simulator route veto as cmd/api
// (internal/routepolicy). It needs no infrastructure: no handler dials a database.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/partnerportal"
	"github.com/gaborage/go-bricks-demo-project/internal/routepolicy"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// defaultConfigFile is resolved against the working directory, the repository root
// under make tenant-family-demo. It sits beside the binary rather than at the root,
// so it can never match the config.<APP_ENV>.yaml overlay pattern cmd/api loads.
const defaultConfigFile = "cmd/tenant-family-demo/config.yaml"

// envOverrides are the only environment variables the loader reads, each mapped to
// the key it sets. Everything else comes from the file, so an unrelated variable in
// the shell (DATABASE_HOST from a cmd/api session, say) cannot reshape this binary.
// APP_ENV is allowed so a deploy can leave development, which drops the
// tenant-owned family and its token simulator (internal/modules/partnerportal).
var envOverrides = map[string]string{
	"SERVER_PORT":       "server.port",
	"LOG_LEVEL":         "log.level",
	"LOG_OUTPUT_FORMAT": "log.output.format",
	"APP_ENV":           "app.env",
}

func main() {
	configFile := flag.String("config", defaultConfigFile, "standalone config file")
	flag.Parse()

	application, log, err := app.NewWithOptions(newAppOptions(*configFile))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize application")
	}
	if err := application.RegisterModule(partnerportal.NewModule()); err != nil {
		log.Fatal().Err(err).Msg("Failed to register module")
	}
	if err := application.Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start application")
	}
}

// newAppOptions loads the binary's own config file and installs the same
// simulator route veto as cmd/api.
func newAppOptions(configFile string) *app.Options {
	return &app.Options{
		ConfigLoader:       func() (*config.Config, error) { return loadConfig(configFile, os.LookupEnv) },
		PostRegisterRoutes: routepolicy.RequireTaggedSimulators,
	}
}

// loadConfig reads one YAML file, applies the envOverrides allowlist, and validates
// the result the way config.Load does. It never reads config.yml or an overlay,
// which is what keeps cmd/api's config layout untouched.
//
// config.LoadFromMap adds the framework defaults and decodes with config.Load's
// decoder, but does not validate, so config.Validate runs here.
func loadConfig(path string, lookupEnv func(string) (string, bool)) (*config.Config, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	for envVar, key := range envOverrides {
		if value, ok := lookupEnv(envVar); ok {
			if err := k.Set(key, value); err != nil {
				return nil, fmt.Errorf("apply %s: %w", envVar, err)
			}
		}
	}
	cfg, err := config.LoadFromMap(k.Raw())
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration in %s: %w", path, err)
	}
	return cfg, nil
}
