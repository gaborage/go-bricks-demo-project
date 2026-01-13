// Package domain contains the domain models for the analytics module.
package domain

import (
	"time"
)

// ProductView represents a single product view event.
type ProductView struct {
	ID        string    `json:"id"`
	ProductID string    `json:"productId"`
	ViewedAt  time.Time `json:"viewedAt"`
	UserAgent string    `json:"userAgent,omitempty"`
	IPAddress string    `json:"ipAddress,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Referrer  string    `json:"referrer,omitempty"`
}

// ProductViewEntity is the database entity for product views.
type ProductViewEntity struct {
	ID        string    `db:"id"`
	ProductID string    `db:"product_id"`
	ViewedAt  time.Time `db:"viewed_at"`
	UserAgent string    `db:"user_agent"`
	IPAddress string    `db:"ip_address"`
	SessionID string    `db:"session_id"`
	Referrer  string    `db:"referrer"`
}

// TableName returns the database table name.
func (e *ProductViewEntity) TableName() string {
	return "product_views"
}

// NewProductView creates a new product view event with the current timestamp.
func NewProductView(productID, userAgent, ipAddress, sessionID, referrer string) *ProductView {
	return &ProductView{
		ProductID: productID,
		ViewedAt:  time.Now().UTC(),
		UserAgent: userAgent,
		IPAddress: ipAddress,
		SessionID: sessionID,
		Referrer:  referrer,
	}
}

// ToEntity converts the domain model to a database entity.
func (pv *ProductView) ToEntity() *ProductViewEntity {
	return &ProductViewEntity{
		ID:        pv.ID,
		ProductID: pv.ProductID,
		ViewedAt:  pv.ViewedAt,
		UserAgent: pv.UserAgent,
		IPAddress: pv.IPAddress,
		SessionID: pv.SessionID,
		Referrer:  pv.Referrer,
	}
}

// ToProductView converts a database entity to a domain model.
func ToProductView(e *ProductViewEntity) *ProductView {
	return &ProductView{
		ID:        e.ID,
		ProductID: e.ProductID,
		ViewedAt:  e.ViewedAt,
		UserAgent: e.UserAgent,
		IPAddress: e.IPAddress,
		SessionID: e.SessionID,
		Referrer:  e.Referrer,
	}
}

// ViewStats represents aggregated view statistics for a product.
type ViewStats struct {
	ProductID     string    `json:"productId"`
	TotalViews    int64     `json:"totalViews"`
	ViewsToday    int64     `json:"viewsToday"`
	ViewsThisWeek int64     `json:"viewsThisWeek"`
	LastViewedAt  time.Time `json:"lastViewedAt,omitempty"`
}

// TopProductStats represents a product in the top-viewed list.
type TopProductStats struct {
	ProductID  string `json:"productId"`
	TotalViews int64  `json:"totalViews"`
}
