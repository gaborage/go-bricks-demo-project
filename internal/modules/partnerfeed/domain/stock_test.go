package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// server.NewValidator is built on the same rule set the typed consumer runs
// (go-bricks internal/validation), so these tests exercise the boundary the
// framework enforces before onStockUpdated is called.
func validate(t *testing.T, body string) error {
	t.Helper()
	var evt StockUpdated
	require.NoError(t, json.Unmarshal([]byte(body), &evt), "the fixture must be decodable JSON")
	return server.NewValidator().Validate(evt)
}

const validBody = `{"eventId":"evt-1","partnerId":"acme-supply","sku":"SKU-001","quantity":12,"occurredAt":"2026-09-24T10:00:00Z"}`

func TestStockUpdatedAcceptsAWellFormedPartnerEvent(t *testing.T) {
	assert.NoError(t, validate(t, validBody))
}

func TestStockUpdatedAcceptsZeroQuantity(t *testing.T) {
	body := strings.Replace(validBody, `"quantity":12`, `"quantity":0`, 1)
	assert.NoError(t, validate(t, body), "out of stock is a valid stock level")
}

func TestStockUpdatedRejectsPartnerMistakes(t *testing.T) {
	cases := map[string]string{
		"missing eventId":    strings.Replace(validBody, `"eventId":"evt-1",`, ``, 1),
		"missing partnerId":  strings.Replace(validBody, `"partnerId":"acme-supply",`, ``, 1),
		"missing sku":        strings.Replace(validBody, `"sku":"SKU-001",`, ``, 1),
		"missing quantity":   strings.Replace(validBody, `"quantity":12,`, ``, 1),
		"negative quantity":  strings.Replace(validBody, `"quantity":12`, `"quantity":-1`, 1),
		"missing occurredAt": strings.Replace(validBody, `,"occurredAt":"2026-09-24T10:00:00Z"`, ``, 1),
		"oversized sku":      strings.Replace(validBody, `"SKU-001"`, `"`+strings.Repeat("x", 65)+`"`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			require.NotEqual(t, validBody, body, "the fixture edit must have applied")
			assert.Error(t, validate(t, body))
		})
	}
}
