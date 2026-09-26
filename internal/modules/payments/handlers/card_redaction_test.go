package handlers

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/gaborage/go-bricks/logger"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The holder validBody() carries. testPAN (handlers_test.go) is the documented
// Visa test PAN every payments fixture uses — never a real card.
const redactFixtureHolder = "Ada Lovelace"

// longDigitRun matches anything PAN-shaped: a log line may carry at most four
// consecutive digits (last4, the fixture amount).
var longDigitRun = regexp.MustCompile(`\d{5,}`)

// appFilterConfig mirrors the filter the app's logger runs with: the framework
// defaults plus the `pan` needle config.development.yaml adds under
// log.sensitivefields, merged the way the framework merges YAML needles.
func appFilterConfig() *logger.FilterConfig {
	cfg := logger.DefaultFilterConfig()
	cfg.SensitiveFields = append(cfg.SensitiveFields, "pan")
	return cfg
}

// captureFilteredLogger builds a framework logger with the given filter and
// swaps only its sink: WithContext honors a zerolog logger carried in ctx and
// keeps the filter. The sink writes no timestamp or caller, so every digit in
// the buffer came from the logged value.
func captureFilteredLogger(t *testing.T, filter *logger.FilterConfig) (logger.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	sink := zerolog.New(&buf)
	return logger.NewWithFilter("info", false, filter).WithContext(sink.WithContext(t.Context())), &buf
}

// The decoded HTTP card reaching a filtered logger — directly, nested in the
// whole request body, or through WithFields — renders the domain card's
// RedactedForLog shape. The framework-default filter has no `pan` needle, so
// that row proves the hook itself masks the PAN; no filter has a needle for
// the holder, so every row proves the hook drops it.
func TestCardRequestLogsItsRedactedShape(t *testing.T) {
	body := validBody()
	card := body.Card
	require.Equal(t, testPAN, card.PAN)
	require.Equal(t, redactFixtureHolder, card.Holder)

	filters := []struct {
		name   string
		config func() *logger.FilterConfig
	}{
		{"app filter", appFilterConfig},
		{"framework default filter", logger.DefaultFilterConfig},
	}
	doors := []struct {
		name string
		log  func(logger.Logger)
	}{
		{"Interface", func(l logger.Logger) { l.Info().Interface("card", card).Msg("card") }},
		{"Interface nested in the request body", func(l logger.Logger) { l.Info().Interface("request", body).Msg("request") }},
		{"WithFields", func(l logger.Logger) { l.WithFields(map[string]any{"card": card}).Info().Msg("card") }},
	}

	for _, filter := range filters {
		for _, door := range doors {
			t.Run(filter.name+"/"+door.name, func(t *testing.T) {
				log, buf := captureFilteredLogger(t, filter.config())
				door.log(log)
				line := buf.String()
				require.NotEmpty(t, line)

				assert.NotContains(t, line, testPAN)
				assert.NotRegexp(t, longDigitRun, line, "no digit run longer than 4 may reach the sink")
				assert.NotContains(t, line, redactFixtureHolder, "the holder's name is not part of the log view")
				assert.Contains(t, line, `"last4":"1111"`)
				assert.NotContains(t, line, "expMonth", "the expiry is not part of the log view")
				assert.NotContains(t, line, "expYear", "the expiry is not part of the log view")
				assert.NotContains(t, line, "2030", "the expiry is not part of the log view")
			})
		}
	}
}

// Last4 guards short input, so the hook never panics on the logging path —
// the filter runs it with no recover.
func TestCardRequestRedactedForLogShortPAN(t *testing.T) {
	assert.Equal(t, map[string]any{"last4": ""}, CardRequest{PAN: "411"}.RedactedForLog())
}
