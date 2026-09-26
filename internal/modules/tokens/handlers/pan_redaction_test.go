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

// The documented Visa test PAN every tokens fixture uses — never a real card.
const redactFixturePAN = "4111111111111111"

// longDigitRun matches anything PAN-shaped: a log line may carry at most the
// last four digits.
var longDigitRun = regexp.MustCompile(`\d{5,}`)

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

// Each PAN-bearing request logged whole renders only its last four digits. The
// app filter adds the `pan` needle config.development.yaml sets under
// log.sensitivefields; the framework-default filter has no such needle, so that
// row proves the RedactedForLog hook itself keeps the PAN out.
func TestPANBearingRequestsLogOnlyLast4(t *testing.T) {
	appFilter := func() *logger.FilterConfig {
		cfg := logger.DefaultFilterConfig()
		cfg.SensitiveFields = append(cfg.SensitiveFields, "pan")
		return cfg
	}
	filters := []struct {
		name   string
		config func() *logger.FilterConfig
	}{
		{"app filter", appFilter},
		{"framework default filter", logger.DefaultFilterConfig},
	}
	requests := []struct {
		name string
		req  any
	}{
		{"TokenizeRequest", TokenizeRequest{PAN: redactFixturePAN}},
		{"PeerSimRequest", PeerSimRequest{PAN: redactFixturePAN}},
		{"RelayRequest", RelayRequest{PAN: redactFixturePAN}},
		{"MLERelayRequest", MLERelayRequest{PAN: redactFixturePAN}},
	}

	for _, filter := range filters {
		for _, tc := range requests {
			t.Run(filter.name+"/"+tc.name, func(t *testing.T) {
				log, buf := captureFilteredLogger(t, filter.config())
				log.Info().Interface("request", tc.req).Msg("request received")
				line := buf.String()
				require.NotEmpty(t, line)

				assert.NotContains(t, line, redactFixturePAN)
				assert.NotRegexp(t, longDigitRun, line, "no digit run longer than 4 may reach the sink")
				assert.Contains(t, line, `"request":{"last4":"1111"}`)
			})
		}
	}
}

// A PAN too short to have four digits renders an empty last4 rather than
// panicking: the filter runs the hook with no recover.
func TestPANLogViewShortInput(t *testing.T) {
	assert.Equal(t, map[string]any{"last4": ""}, RelayRequest{PAN: "411"}.RedactedForLog())
}
