package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/google/uuid"
)

// Activity action values carried by ProductActivity.Action: the bare verbs of the
// product.* outbox event types this service already publishes.
const (
	ActivityCreated = "created"
	ActivityUpdated = "updated"
	ActivityDeleted = "deleted"
)

// ProductActivity is what this service hands to an ActivityRecorder after a
// successful write.
//
// It is declared HERE, on the consumer side of the seam, rather than in the
// activity module that happens to implement it today: products is the core module
// and must compile — with the legacy module that reuses it — in a build that
// carries no demo activity module at all. The recorder's implementation adapts
// this type onto whatever wire format it publishes.
type ProductActivity struct {
	ProductID  string
	Action     string
	Name       string
	Price      float64
	OccurredAt time.Time
}

// ActivityRecorder is the narrow seam this service uses to mirror each successful
// write onto the product-activity super stream. It is implemented by the activity
// module and injected in cmd/api/main.go — the same cross-module reuse the legacy
// module demonstrates, with the direction inverted.
//
// It returns nothing on purpose. The stream lane is best-effort demo telemetry:
// the implementation logs and swallows its own failures so a broker hiccup can
// never fail an HTTP request whose database work already committed. The
// transactional outbox below stays the reliable path for product lifecycle
// events.
type ActivityRecorder interface {
	RecordActivity(ctx context.Context, evt ProductActivity)
}

type ProductService struct {
	repository repository.Repository
	logger     logger.Logger
	outbox     app.OutboxPublisher
	getDB      func(context.Context) (database.Interface, error)
	activity   ActivityRecorder
}

// SetActivityRecorder injects the stream-lane recorder. Call it during startup,
// before app.Run() spawns the server goroutine: the field is written once there
// and read from request goroutines afterwards, so the goroutine's own
// happens-before is what makes the handoff safe.
//
// Leaving it unset is a supported configuration — the legacy module builds this
// service without one, and so do the unit tests.
func (s *ProductService) SetActivityRecorder(r ActivityRecorder) {
	s.activity = r
}

// recordActivity mirrors a successful write onto the stream lane. Nil-safe: a
// service with no recorder wired simply skips it. The guard is a plain interface
// comparison, so the wiring side must never hand it a typed nil — see the
// activity module's Recorder(), which returns the interface and an explicit nil.
func (s *ProductService) recordActivity(ctx context.Context, productID, action, name string, price float64) {
	if s.activity == nil {
		return
	}
	s.activity.RecordActivity(ctx, ProductActivity{
		ProductID:  productID,
		Action:     action,
		Name:       name,
		Price:      price,
		OccurredAt: time.Now().UTC(),
	})
}

func NewService(repo repository.Repository, log logger.Logger, outbox app.OutboxPublisher, getDB func(context.Context) (database.Interface, error)) *ProductService {
	return &ProductService{
		repository: repo,
		logger:     log,
		outbox:     outbox,
		getDB:      getDB,
	}
}

// CreateProduct creates a new product with validation.
// When an outbox publisher is configured, the insert and a "product.created"
// event are committed in the same database transaction (dual-write pattern).
func (s *ProductService) CreateProduct(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error) {
	// Validate name
	if err := validateName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// Validate price
	if price < 0 {
		return nil, fmt.Errorf("%w: price must be non-negative", ErrValidation)
	}

	// Validate image URL if provided
	if imageURL != "" {
		if err := validateURL(imageURL); err != nil {
			return nil, fmt.Errorf("%w: invalid image URL: %v", ErrValidation, err)
		}
	}

	// Generate UUID for new product
	id := uuid.New().String()

	// Create product domain object
	product := domain.New(id, name, description, price, imageURL)

	// Validate domain object
	if err := product.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// Transactional path: insert + outbox event in one transaction
	if s.outbox != nil && s.getDB != nil {
		if err := s.createWithOutbox(ctx, product); err != nil {
			s.logger.Error().Err(err).Str("productID", id).Msg("Failed to create product")
			return nil, fmt.Errorf("%w: failed to create product: %v", ErrInternal, err)
		}
	} else {
		// Non-transactional fallback (legacy module, tests without outbox)
		if err := s.repository.Create(ctx, product); err != nil {
			s.logger.Error().Err(err).Str("productID", id).Msg("Failed to create product")
			return nil, fmt.Errorf("%w: failed to create product: %v", ErrInternal, err)
		}
	}

	s.recordActivity(ctx, product.ID, ActivityCreated, product.Name, product.Price)

	s.logger.Info().Str("productID", id).Str("name", name).Msg("Product created successfully")
	return product, nil
}

// createWithOutbox wraps insert + outbox publish in a single transaction.
func (s *ProductService) createWithOutbox(ctx context.Context, product *domain.Product) error {
	db, err := s.getDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	if err := s.repository.CreateTx(ctx, tx, product); err != nil {
		return err
	}

	_, err = s.outbox.Publish(ctx, tx, &app.OutboxEvent{
		EventType:   "product.created",
		AggregateID: product.ID,
		Payload:     product,
	})
	if err != nil {
		return fmt.Errorf("failed to publish outbox event: %w", err)
	}

	return tx.Commit(ctx)
}

// GetProductByID retrieves a product by its ID
func (s *ProductService) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	product, err := s.repository.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, err
		}
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to get product")
		return nil, fmt.Errorf("%w: failed to get product: %v", ErrInternal, err)
	}

	return product, nil
}

// validateName checks if the product name is valid
func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("product name is required")
	}
	if len(name) > 150 {
		return fmt.Errorf("product name must be less than 150 characters")
	}
	return nil
}

// validateURL checks if the URL is valid
func validateURL(urlStr string) error {
	if urlStr == "" {
		return nil
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return err
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must have a valid host")
	}

	return nil
}

// ListProducts retrieves a paginated list of products
func (s *ProductService) ListProducts(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
	// Validate pagination parameters
	if page < 1 {
		return nil, 0, fmt.Errorf("%w: page must be greater than 0", ErrValidation)
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, 0, fmt.Errorf("%w: pageSize must be between 1 and 100", ErrValidation)
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Fetch from repository
	products, total, err := s.repository.List(ctx, pageSize, offset)
	if err != nil {
		s.logger.Error().Err(err).Int("page", page).Int("pageSize", pageSize).Msg("Failed to list products")
		return nil, 0, fmt.Errorf("%w: failed to list products: %v", ErrInternal, err)
	}

	return products, total, nil
}

// UpdateProduct performs a partial update on a product.
// After a successful update, publishes a "product.updated" event to the outbox
// (non-transactional — the single UPDATE statement is inherently atomic).
func (s *ProductService) UpdateProduct(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
	// Build update map with only provided fields
	updates := make(map[string]any)

	if name != nil {
		if err := validateName(*name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidation, err)
		}
		updates["name"] = *name
	}

	if description != nil {
		updates["description"] = *description
	}

	if price != nil {
		if *price < 0 {
			return nil, fmt.Errorf("%w: price must be non-negative", ErrValidation)
		}
		updates["price"] = *price
	}

	if imageURL != nil {
		if *imageURL != "" {
			if err := validateURL(*imageURL); err != nil {
				return nil, fmt.Errorf("%w: invalid image URL: %v", ErrValidation, err)
			}
		}
		updates["image_url"] = *imageURL
	}

	// Return error if no fields to update
	if len(updates) == 0 {
		return nil, fmt.Errorf("%w: no fields to update", ErrValidation)
	}

	// Always update the updated_date
	updates["updated_date"] = "NOW()"

	// Perform update in repository
	if err := s.repository.Update(ctx, id, updates); err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, err
		}
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to update product")
		return nil, fmt.Errorf("%w: failed to update product: %v", ErrInternal, err)
	}

	// Fetch and return updated product
	product, err := s.repository.GetByID(ctx, id)
	if err != nil {
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to fetch updated product")
		return nil, fmt.Errorf("%w: failed to fetch updated product: %v", ErrInternal, err)
	}

	// Publish outbox event after successful update (best-effort, non-transactional)
	s.publishEvent(ctx, "product.updated", id, product)

	s.recordActivity(ctx, product.ID, ActivityUpdated, product.Name, product.Price)

	s.logger.Info().Str("productID", id).Msg("Product updated successfully")
	return product, nil
}

// DeleteProduct removes a product.
// When an outbox publisher is configured, the delete and a "product.deleted"
// event are committed in the same database transaction.
func (s *ProductService) DeleteProduct(ctx context.Context, id string) error {
	if s.outbox != nil && s.getDB != nil {
		if err := s.deleteWithOutbox(ctx, id); err != nil {
			if errors.Is(err, repository.ErrProductNotFound) {
				return err
			}
			s.logger.Error().Err(err).Str("productID", id).Msg("Failed to delete product")
			return fmt.Errorf("%w: failed to delete product: %v", ErrInternal, err)
		}
	} else {
		if err := s.repository.Delete(ctx, id); err != nil {
			if errors.Is(err, repository.ErrProductNotFound) {
				return err
			}
			s.logger.Error().Err(err).Str("productID", id).Msg("Failed to delete product")
			return fmt.Errorf("%w: failed to delete product: %v", ErrInternal, err)
		}
	}

	// The row is gone, so name and price are no longer knowable here; the
	// projection tallies the delete by product id.
	s.recordActivity(ctx, id, ActivityDeleted, "", 0)

	s.logger.Info().Str("productID", id).Msg("Product deleted successfully")
	return nil
}

// deleteWithOutbox wraps delete + outbox publish in a single transaction.
func (s *ProductService) deleteWithOutbox(ctx context.Context, id string) error {
	db, err := s.getDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	if err := s.repository.DeleteTx(ctx, tx, id); err != nil {
		return err
	}

	_, err = s.outbox.Publish(ctx, tx, &app.OutboxEvent{
		EventType:   "product.deleted",
		AggregateID: id,
		Payload:     map[string]string{"id": id},
	})
	if err != nil {
		return fmt.Errorf("failed to publish outbox event: %w", err)
	}

	return tx.Commit(ctx)
}

// publishEvent is a best-effort outbox publish (non-transactional).
// Used for updates where the single UPDATE is already atomic.
func (s *ProductService) publishEvent(ctx context.Context, eventType, aggregateID string, payload any) {
	if s.outbox == nil || s.getDB == nil {
		return
	}

	db, err := s.getDB(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Str("eventType", eventType).Msg("Failed to get DB for outbox event")
		return
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Str("eventType", eventType).Msg("Failed to begin tx for outbox event")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = s.outbox.Publish(ctx, tx, &app.OutboxEvent{
		EventType:   eventType,
		AggregateID: aggregateID,
		Payload:     payload,
	})
	if err != nil {
		s.logger.Warn().Err(err).Str("eventType", eventType).Msg("Failed to publish outbox event")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Warn().Err(err).Str("eventType", eventType).Msg("Failed to commit outbox event")
	}
}
