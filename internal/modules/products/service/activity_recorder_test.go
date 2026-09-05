package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
)

var errRepositoryFailure = errors.New("database error")

const renamedProductName = "Renamed"

// recordingRecorder captures what the product service mirrors onto the stream lane.
type recordingRecorder struct {
	mu     sync.Mutex
	events []ProductActivity
}

//nolint:gocritic // hugeParam: the value signature is the ActivityRecorder seam under test.
func (r *recordingRecorder) RecordActivity(_ context.Context, evt ProductActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *recordingRecorder) recorded() []ProductActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ProductActivity(nil), r.events...)
}

// The legacy module and every unit test build this service without a recorder, so
// the seam must be a silent no-op when it is not wired.
func TestProductServiceWithoutActivityRecorder(t *testing.T) {
	svc := NewService(&mockRepository{}, newMockLogger(), nil, nil)
	ctx := context.Background()

	product, err := svc.CreateProduct(ctx, testProductName, testDescription, 10.5, testImageURL)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if product == nil {
		t.Fatal("CreateProduct returned no product")
	}
	if err := svc.DeleteProduct(ctx, product.ID); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}
}

func TestProductServiceRecordsActivityOnCreate(t *testing.T) {
	recorder := &recordingRecorder{}
	svc := NewService(&mockRepository{}, newMockLogger(), nil, nil)
	svc.SetActivityRecorder(recorder)

	product, err := svc.CreateProduct(context.Background(), testProductName, testDescription, 10.5, testImageURL)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	events := recorder.recorded()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if events[0].Action != ActivityCreated {
		t.Errorf("Action = %q, want %q", events[0].Action, ActivityCreated)
	}
	if events[0].ProductID != product.ID {
		t.Errorf("ProductID = %q, want %q", events[0].ProductID, product.ID)
	}
	if events[0].Name != testProductName {
		t.Errorf("Name = %q, want %q", events[0].Name, testProductName)
	}
	if events[0].Price != 10.5 {
		t.Errorf("Price = %v, want 10.5", events[0].Price)
	}
	if events[0].OccurredAt.IsZero() {
		t.Error("OccurredAt is zero; the consumer's validate tags require it")
	}
}

func TestProductServiceRecordsActivityOnUpdate(t *testing.T) {
	recorder := &recordingRecorder{}
	updated := &domain.Product{ID: testID, Name: renamedProductName, Price: 3.25}
	repo := &mockRepository{
		getByIDFunc: func(_ context.Context, _ string) (*domain.Product, error) { return updated, nil },
	}
	svc := NewService(repo, newMockLogger(), nil, nil)
	svc.SetActivityRecorder(recorder)

	newName := renamedProductName
	if _, err := svc.UpdateProduct(context.Background(), testID, &newName, nil, nil, nil); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	events := recorder.recorded()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if events[0].Action != ActivityUpdated {
		t.Errorf("Action = %q, want %q", events[0].Action, ActivityUpdated)
	}
	if events[0].ProductID != testID {
		t.Errorf("ProductID = %q, want %q", events[0].ProductID, testID)
	}
	if events[0].Name != renamedProductName {
		t.Errorf("Name = %q, want %q", events[0].Name, renamedProductName)
	}
}

func TestProductServiceRecordsActivityOnDelete(t *testing.T) {
	recorder := &recordingRecorder{}
	svc := NewService(&mockRepository{}, newMockLogger(), nil, nil)
	svc.SetActivityRecorder(recorder)

	if err := svc.DeleteProduct(context.Background(), testID); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}

	events := recorder.recorded()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if events[0].Action != ActivityDeleted {
		t.Errorf("Action = %q, want %q", events[0].Action, ActivityDeleted)
	}
	if events[0].ProductID != testID {
		t.Errorf("ProductID = %q, want %q", events[0].ProductID, testID)
	}
}

// A failing write must not reach the stream lane: the projection would then count
// an event the database never recorded.
func TestProductServiceSkipsActivityWhenTheWriteFails(t *testing.T) {
	recorder := &recordingRecorder{}
	repo := &mockRepository{
		deleteFunc: func(_ context.Context, _ string) error { return errRepositoryFailure },
	}
	svc := NewService(repo, newMockLogger(), nil, nil)
	svc.SetActivityRecorder(recorder)

	if err := svc.DeleteProduct(context.Background(), testID); err == nil {
		t.Fatal("DeleteProduct returned nil, want the repository failure")
	}
	if got := len(recorder.recorded()); got != 0 {
		t.Errorf("recorded %d events after a failed delete, want 0", got)
	}
}
