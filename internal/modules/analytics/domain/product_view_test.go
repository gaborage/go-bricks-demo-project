package domain

import (
	"testing"
	"time"
)

const (
	testProductID = "product-123"
	testUserAgent = "Mozilla/5.0"
	testIPAddress = "127.0.0.1"
	testSessionID = "session-abc"
	testReferrer  = "https://example.com"
)

func TestNewProductView(t *testing.T) {
	before := time.Now().UTC()
	view := NewProductView(testProductID, testUserAgent, testIPAddress, testSessionID, testReferrer)
	after := time.Now().UTC()

	if view.ProductID != testProductID {
		t.Errorf("ProductID = %v, want %v", view.ProductID, testProductID)
	}
	if view.UserAgent != testUserAgent {
		t.Errorf("UserAgent = %v, want %v", view.UserAgent, testUserAgent)
	}
	if view.IPAddress != testIPAddress {
		t.Errorf("IPAddress = %v, want %v", view.IPAddress, testIPAddress)
	}
	if view.SessionID != testSessionID {
		t.Errorf("SessionID = %v, want %v", view.SessionID, testSessionID)
	}
	if view.Referrer != testReferrer {
		t.Errorf("Referrer = %v, want %v", view.Referrer, testReferrer)
	}
	if view.ViewedAt.Before(before) || view.ViewedAt.After(after) {
		t.Errorf("ViewedAt = %v, want between %v and %v", view.ViewedAt, before, after)
	}
	if view.ViewedAt.Location() != time.UTC {
		t.Errorf("ViewedAt location = %v, want UTC", view.ViewedAt.Location())
	}
	if view.ID != "" {
		t.Errorf("ID = %v, want empty (assigned by repository)", view.ID)
	}
}

func TestProductViewToEntity(t *testing.T) {
	now := time.Now().UTC()
	view := &ProductView{
		ID:        "view-1",
		ProductID: testProductID,
		ViewedAt:  now,
		UserAgent: testUserAgent,
		IPAddress: testIPAddress,
		SessionID: testSessionID,
		Referrer:  testReferrer,
	}

	entity := view.ToEntity()

	if entity.ID != view.ID {
		t.Errorf("ID = %v, want %v", entity.ID, view.ID)
	}
	if entity.ProductID != view.ProductID {
		t.Errorf("ProductID = %v, want %v", entity.ProductID, view.ProductID)
	}
	if !entity.ViewedAt.Equal(view.ViewedAt) {
		t.Errorf("ViewedAt = %v, want %v", entity.ViewedAt, view.ViewedAt)
	}
	if entity.UserAgent != view.UserAgent {
		t.Errorf("UserAgent = %v, want %v", entity.UserAgent, view.UserAgent)
	}
	if entity.IPAddress != view.IPAddress {
		t.Errorf("IPAddress = %v, want %v", entity.IPAddress, view.IPAddress)
	}
	if entity.SessionID != view.SessionID {
		t.Errorf("SessionID = %v, want %v", entity.SessionID, view.SessionID)
	}
	if entity.Referrer != view.Referrer {
		t.Errorf("Referrer = %v, want %v", entity.Referrer, view.Referrer)
	}
}

func TestToProductView(t *testing.T) {
	now := time.Now().UTC()
	entity := &ProductViewEntity{
		ID:        "view-1",
		ProductID: testProductID,
		ViewedAt:  now,
		UserAgent: testUserAgent,
		IPAddress: testIPAddress,
		SessionID: testSessionID,
		Referrer:  testReferrer,
	}

	view := ToProductView(entity)

	if view.ID != entity.ID {
		t.Errorf("ID = %v, want %v", view.ID, entity.ID)
	}
	if view.ProductID != entity.ProductID {
		t.Errorf("ProductID = %v, want %v", view.ProductID, entity.ProductID)
	}
	if !view.ViewedAt.Equal(entity.ViewedAt) {
		t.Errorf("ViewedAt = %v, want %v", view.ViewedAt, entity.ViewedAt)
	}
	if view.UserAgent != entity.UserAgent {
		t.Errorf("UserAgent = %v, want %v", view.UserAgent, entity.UserAgent)
	}
	if view.IPAddress != entity.IPAddress {
		t.Errorf("IPAddress = %v, want %v", view.IPAddress, entity.IPAddress)
	}
	if view.SessionID != entity.SessionID {
		t.Errorf("SessionID = %v, want %v", view.SessionID, entity.SessionID)
	}
	if view.Referrer != entity.Referrer {
		t.Errorf("Referrer = %v, want %v", view.Referrer, entity.Referrer)
	}
}

func TestProductViewEntityTableName(t *testing.T) {
	entity := &ProductViewEntity{}
	if got := entity.TableName(); got != "product_views" {
		t.Errorf("TableName() = %v, want %v", got, "product_views")
	}
}

func TestProductViewRoundTrip(t *testing.T) {
	original := NewProductView(testProductID, testUserAgent, testIPAddress, testSessionID, testReferrer)
	original.ID = "round-trip-id"

	roundTripped := ToProductView(original.ToEntity())

	if roundTripped.ID != original.ID {
		t.Errorf("ID = %v, want %v", roundTripped.ID, original.ID)
	}
	if roundTripped.ProductID != original.ProductID {
		t.Errorf("ProductID = %v, want %v", roundTripped.ProductID, original.ProductID)
	}
	if !roundTripped.ViewedAt.Equal(original.ViewedAt) {
		t.Errorf("ViewedAt = %v, want %v", roundTripped.ViewedAt, original.ViewedAt)
	}
}
