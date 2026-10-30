package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/service"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

const testProductID = "product-a"

// mockProjection stubs the narrow service contract the handlers depend on.
type mockProjection struct {
	snapshot       service.Snapshot
	snapshotLimit  int
	slice          service.ProductSlice
	sliceProductID string
	publishErr     error
	publishedKey   string
	publishedBytes []byte
	publishCalls   int
}

func (m *mockProjection) Snapshot(limit int) service.Snapshot {
	m.snapshotLimit = limit
	return m.snapshot
}

func (m *mockProjection) ProductSlice(productID string) service.ProductSlice {
	m.sliceProductID = productID
	return m.slice
}

func (m *mockProjection) PublishRaw(_ context.Context, routingKey string, data []byte) error {
	m.publishCalls++
	m.publishedKey = routingKey
	m.publishedBytes = data
	return m.publishErr
}

func newTestConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test"},
	}
}

func newTestContext() server.HandlerContext {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	return server.NewHandlerContextForTest(httptest.NewRecorder(), req, newTestConfig())
}

func newTestHandler(svc ActivityProjection) *Handler {
	return NewHandler(svc, logger.New("error", false))
}

func TestGetActivityReturnsTheProjectionSnapshot(t *testing.T) {
	stub := &mockProjection{snapshot: service.Snapshot{
		SuperStream:     domain.SuperStream,
		Partitions:      domain.PartitionCount,
		ConsumerName:    domain.ConsumerName,
		Delivered:       7,
		ProductCounts:   map[string]map[string]int64{testProductID: {domain.ActionCreated: 1}},
		PartitionCounts: map[string]int64{domain.SuperStream + "-1": 7},
		Recent: []service.RecentEvent{{
			ProductID:  testProductID,
			Action:     domain.ActionCreated,
			Partition:  domain.SuperStream + "-1",
			Offset:     42,
			OccurredAt: time.Now().UTC(),
		}},
	}}

	got, apiErr := newTestHandler(stub).GetActivity(SnapshotRequest{Limit: 5}, newTestContext())
	if apiErr != nil {
		t.Fatalf("GetActivity returned error: %v", apiErr)
	}
	if stub.snapshotLimit != 5 {
		t.Errorf("Snapshot called with limit %d, want 5", stub.snapshotLimit)
	}
	if got.Delivered != 7 {
		t.Errorf("Delivered = %d, want 7", got.Delivered)
	}
	if got.SuperStream != domain.SuperStream {
		t.Errorf("SuperStream = %q, want %q", got.SuperStream, domain.SuperStream)
	}
	if len(got.Recent) != 1 || got.Recent[0].Offset != 42 {
		t.Errorf("Recent = %+v, want one event at offset 42", got.Recent)
	}
}

func TestGetProductActivityReturnsTheProductSlice(t *testing.T) {
	stub := &mockProjection{slice: service.ProductSlice{
		ProductID: testProductID,
		Delivered: 2,
		Counts:    map[string]int64{domain.ActionCreated: 1, domain.ActionUpdated: 1},
		Recent:    []service.RecentEvent{{ProductID: testProductID, Action: domain.ActionUpdated, Offset: 9}},
	}}

	got, apiErr := newTestHandler(stub).GetProductActivity(ProductActivityRequest{ID: testProductID}, newTestContext())
	if apiErr != nil {
		t.Fatalf("GetProductActivity returned error: %v", apiErr)
	}
	if stub.sliceProductID != testProductID {
		t.Errorf("ProductSlice called with %q, want %q", stub.sliceProductID, testProductID)
	}
	if got.ProductID != testProductID || got.Delivered != 2 || got.Counts[domain.ActionUpdated] != 1 {
		t.Errorf("slice = %+v, want the stub's slice for %q", got, testProductID)
	}
	if len(got.Recent) != 1 || got.Recent[0].Offset != 9 {
		t.Errorf("Recent = %+v, want one event at offset 9", got.Recent)
	}
}

// An id the projection has not seen is not a 404: the projection cannot know
// whether the product exists, so it answers what it has seen, which is nothing.
func TestGetProductActivityForAnUnseenIDIsNotAnError(t *testing.T) {
	stub := &mockProjection{slice: service.ProductSlice{
		ProductID: "unseen",
		Counts:    map[string]int64{},
		Recent:    []service.RecentEvent{},
	}}

	got, apiErr := newTestHandler(stub).GetProductActivity(ProductActivityRequest{ID: "unseen"}, newTestContext())
	if apiErr != nil {
		t.Fatalf("GetProductActivity returned %v for an unseen id, want an empty slice", apiErr)
	}
	if got.Delivered != 0 || len(got.Counts) != 0 || len(got.Recent) != 0 {
		t.Errorf("slice = %+v, want an empty one", got)
	}
}

func TestPublishPoisonUsesTheRequestedRoutingKey(t *testing.T) {
	stub := &mockProjection{}

	got, apiErr := newTestHandler(stub).PublishPoison(PoisonRequest{ProductID: testProductID}, newTestContext())
	if apiErr != nil {
		t.Fatalf("PublishPoison returned error: %v", apiErr)
	}
	if stub.publishCalls != 1 {
		t.Fatalf("PublishRaw called %d times, want 1", stub.publishCalls)
	}
	if stub.publishedKey != testProductID {
		t.Errorf("routing key = %q, want %q", stub.publishedKey, testProductID)
	}
	if string(stub.publishedBytes) != poisonPayload {
		t.Errorf("payload = %q, want %q", stub.publishedBytes, poisonPayload)
	}
	if got.RoutingKey != testProductID || got.SuperStream != domain.SuperStream {
		t.Errorf("response = %+v, want routing key %q on %q", got, testProductID, domain.SuperStream)
	}
}

// A super-stream publish needs a non-empty routing key, so the simulator supplies
// one when the caller does not.
func TestPublishPoisonDefaultsTheRoutingKey(t *testing.T) {
	stub := &mockProjection{}

	got, apiErr := newTestHandler(stub).PublishPoison(PoisonRequest{}, newTestContext())
	if apiErr != nil {
		t.Fatalf("PublishPoison returned error: %v", apiErr)
	}
	if stub.publishedKey != defaultPoisonProductID {
		t.Errorf("routing key = %q, want %q", stub.publishedKey, defaultPoisonProductID)
	}
	if got.RoutingKey != defaultPoisonProductID {
		t.Errorf("response routing key = %q, want %q", got.RoutingKey, defaultPoisonProductID)
	}
}

func TestPublishPoisonErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		publishErr error
		wantStatus int
	}{
		{
			name:       "publisher not bound yet",
			publishErr: service.ErrPublisherUnset,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "broker failure",
			publishErr: errors.New("confirmation timed out"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &mockProjection{publishErr: tc.publishErr}

			_, apiErr := newTestHandler(stub).PublishPoison(PoisonRequest{}, newTestContext())
			if apiErr == nil {
				t.Fatal("PublishPoison returned nil error, want a failure")
			}
			if got := apiErr.HTTPStatus(); got != tc.wantStatus {
				t.Errorf("status = %d, want %d", got, tc.wantStatus)
			}
		})
	}
}
