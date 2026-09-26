package domain

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/gaborage/go-bricks/logger"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// The documented Visa test PAN every payments fixture uses — never a real card.
	redactFixturePAN    = "4111111111111111"
	redactFixtureHolder = "Ada Lovelace"
)

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

// A whole CardDetails reaching a filtered logger — directly, nested in the
// event, or through WithFields — renders its RedactedForLog shape. The
// framework-default filter has no `pan` needle, so that row proves the hook
// itself masks the PAN; the app row proves the hook's keys survive the app's
// needle list.
func TestCardDetailsLogsItsRedactedShape(t *testing.T) {
	card := CardDetails{PAN: redactFixturePAN, ExpMonth: 12, ExpYear: 2030, Holder: redactFixtureHolder}
	evt := PaymentAuthorized{OrderID: "order-demo", Amount: 1999, Currency: "USD", Card: card}

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
		{"Interface nested in the event", func(l logger.Logger) { l.Info().Interface("event", evt).Msg("event") }},
		{"WithFields", func(l logger.Logger) { l.WithFields(map[string]any{"card": card}).Info().Msg("card") }},
	}

	for _, filter := range filters {
		for _, door := range doors {
			t.Run(filter.name+"/"+door.name, func(t *testing.T) {
				log, buf := captureFilteredLogger(t, filter.config())
				door.log(log)
				line := buf.String()
				require.NotEmpty(t, line)

				assert.NotContains(t, line, redactFixturePAN)
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
func TestCardDetailsRedactedForLogShortPAN(t *testing.T) {
	assert.Equal(t, map[string]any{"last4": ""}, CardDetails{PAN: "411"}.RedactedForLog())
}
