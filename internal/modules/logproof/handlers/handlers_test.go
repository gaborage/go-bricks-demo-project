package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testToken = "4111111111111111"

func handlerContext(template string) server.HandlerContext {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/__sim/observability/x/"+testToken, http.NoBody)
	return server.NewHandlerContextForTest(httptest.NewRecorder(), r, nil, server.WithRouteTemplate("/api/v1"+template))
}

func TestWarnAnswersWithTheTemplateNotTheToken(t *testing.T) {
	h := NewHandler(logger.New("disabled", false))

	resp, apiErr := h.Warn(TokenRequest{Token: testToken}, handlerContext(WarnPath))

	require.Nil(t, apiErr)
	assert.Equal(t, "/api/v1"+WarnPath, resp.Route)
	assert.Equal(t, "warn", resp.AccessLevel)
	assert.NotContains(t, resp.Route, testToken)
}

// EscalateSeverity is a no-op on a test context (there is no request log
// context), so this asserts the answer; the escalation itself is proven by the
// in-process server test in the parent package and by make log-correlation-demo.
func TestEscalateAnswers200Shape(t *testing.T) {
	h := NewHandler(logger.New("disabled", false))

	resp, apiErr := h.Escalate(TokenRequest{Token: testToken}, handlerContext(EscalatePath))

	require.Nil(t, apiErr)
	assert.Equal(t, "EscalateSeverity", resp.EscalatedWith)
}

func TestPanicPanicsWithAConstantValue(t *testing.T) {
	h := NewHandler(logger.New("disabled", false))

	assert.PanicsWithValue(t, PanicMessage, func() {
		_, _ = h.Panic(TokenRequest{Token: testToken}, handlerContext(PanicPath))
	})
	assert.NotContains(t, PanicMessage, testToken)
}
