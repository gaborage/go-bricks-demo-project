package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/config"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The config files, as the framework's loader names them. go-bricks reads
// config.yaml and falls back to config.yml only when config.yaml is absent, then
// overlays config.<APP_ENV>.yaml.
const (
	baseConfigFile     = "config.yml"
	shadowConfigFile   = "config.yaml"
	devOverlayFile     = "config.development.yaml"
	baseOnlyKey        = "server.path.base" // set in config.yml only
	baseOnlyKeyValue   = "/api/v1"
	devOnlyKey         = "database.host" // set in config.development.yaml only
	devOnlyKeyValue    = "localhost"
	repoRootFromCmdAPI = "../.."
)

// chdirRepoRoot moves the test into the repository root, where the app reads its
// config files from.
func chdirRepoRoot(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(repoRootFromCmdAPI)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "%s is not the repository root", root)
	t.Chdir(root)
}

// unsetEnv removes an environment variable for the rest of the test and restores
// it afterwards. t.Setenv cannot unset, and an empty value is not the same thing:
// the framework refuses a delivered-empty scalar.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	prev, had := os.LookupEnv(name)
	require.NoError(t, os.Unsetenv(name))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, prev)
		}
	})
}

// With APP_ENV=development the framework's own loader must see both layers: a
// key only the base sets and a key only the development overlay sets. A
// config.yaml added next to config.yml would make the loader skip config.yml
// entirely, and this is where that shows up.
func TestConfigLayoutDevelopmentLoadsBaseAndOverlay(t *testing.T) {
	chdirRepoRoot(t)

	_, err := os.Stat(shadowConfigFile)
	require.ErrorIs(t, err, os.ErrNotExist,
		"%s must not exist: go-bricks would load it instead of %s", shadowConfigFile, baseConfigFile)

	t.Setenv("APP_ENV", config.EnvDevelopment)
	// The asserted keys come from the files, not from the caller's environment.
	unsetEnv(t, "SERVER_PATH_BASE")
	unsetEnv(t, "DATABASE_HOST")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, baseOnlyKeyValue, cfg.String(baseOnlyKey), "%s comes from %s", baseOnlyKey, baseConfigFile)
	assert.Equal(t, devOnlyKeyValue, cfg.String(devOnlyKey), "%s comes from %s", devOnlyKey, devOverlayFile)
}

// The base is loaded in every environment, so it must carry nothing that only
// the local docker-compose stack can use: no connection identity, no key
// material, no development switch, no localhost or certs/ value.
func TestConfigLayoutBaseIsEnvironmentNeutral(t *testing.T) {
	chdirRepoRoot(t)

	k := koanf.New(".")
	require.NoError(t, k.Load(file.Provider(baseConfigFile), yaml.Parser()))

	values := k.All()
	require.NotEmpty(t, values, "%s parsed to nothing", baseConfigFile)
	require.Contains(t, values, baseOnlyKey)

	for key, value := range values {
		assert.Falsef(t, isEnvironmentSpecificKey(key),
			"%s sets %s, which belongs in an environment overlay or env var", baseConfigFile, key)
		for _, s := range stringValues(value) {
			for _, needle := range []string{"localhost", "127.0.0.1", "certs/"} {
				assert.NotContainsf(t, s, needle,
					"%s sets %s to a development-only value", baseConfigFile, key)
			}
		}
	}
}

// isEnvironmentSpecificKey reports whether a key names something each
// environment supplies for itself: connection identity of the infrastructure
// the app dials, key material, or the development-only debug switch. server.*
// is the app's own listener, not infrastructure it dials, so it is exempt.
func isEnvironmentSpecificKey(key string) bool {
	if strings.HasPrefix(key, "keystore.keys.") || key == "app.debug" {
		return true
	}
	if strings.HasPrefix(key, "server.") {
		return false
	}
	switch key[strings.LastIndex(key, ".")+1:] {
	case "host", "port", "username", "password", "url", "uri", "connectionstring":
		return true
	}
	return false
}

// stringValues returns the string scalars in a flattened koanf value.
func stringValues(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, stringValues(item)...)
		}
		return out
	default:
		return nil
	}
}
