package domain

import (
	"errors"
	"testing"
	"time"
)

const (
	testID          = "test-id"
	testName        = "Test Product"
	testDescription = "Test Description"
	testPrice       = 99.99
	testImageURL    = "https://example.com/image.jpg"
	fieldKeyName    = "name"
)

func TestNew(t *testing.T) {
	before := time.Now().UTC()
	product := New(testID, testName, testDescription, testPrice, testImageURL)
	after := time.Now().UTC()

	if product.ID != testID {
		t.Errorf("New() ID = %v, want %v", product.ID, testID)
	}
	if product.Name != testName {
		t.Errorf("New() Name = %v, want %v", product.Name, testName)
	}
	if product.Description != testDescription {
		t.Errorf("New() Description = %v, want %v", product.Description, testDescription)
	}
	if product.Price != testPrice {
		t.Errorf("New() Price = %v, want %v", product.Price, testPrice)
	}
	if product.ImageURL != testImageURL {
		t.Errorf("New() ImageURL = %v, want %v", product.ImageURL, testImageURL)
	}
	if product.CreatedDate.Before(before) || product.CreatedDate.After(after) {
		t.Errorf("New() CreatedDate = %v, want between %v and %v", product.CreatedDate, before, after)
	}
	if !product.CreatedDate.Equal(product.UpdatedDate) {
		t.Errorf("New() CreatedDate = %v, want equal to UpdatedDate %v", product.CreatedDate, product.UpdatedDate)
	}
	if product.CreatedDate.Location() != time.UTC {
		t.Errorf("New() CreatedDate location = %v, want UTC", product.CreatedDate.Location())
	}
}

func TestUpdate(t *testing.T) {
	newName := "Updated Name"
	newDescription := "Updated Description"
	newPrice := 149.99
	newImageURL := "https://example.com/updated.jpg"

	tests := []struct {
		name         string
		updates      map[string]any
		wantName     string
		wantDesc     string
		wantPrice    float64
		wantImageURL string
	}{
		{
			name: "update all fields",
			updates: map[string]any{
				fieldKeyName:  newName,
				"description": newDescription,
				"price":       newPrice,
				FieldImageURL: newImageURL,
			},
			wantName:     newName,
			wantDesc:     newDescription,
			wantPrice:    newPrice,
			wantImageURL: newImageURL,
		},
		{
			name: "update only name",
			updates: map[string]any{
				fieldKeyName: newName,
			},
			wantName:     newName,
			wantDesc:     testDescription,
			wantPrice:    testPrice,
			wantImageURL: testImageURL,
		},
		{
			name:         "empty updates map leaves fields unchanged",
			updates:      map[string]any{},
			wantName:     testName,
			wantDesc:     testDescription,
			wantPrice:    testPrice,
			wantImageURL: testImageURL,
		},
		{
			name: "wrong type for field is ignored",
			updates: map[string]any{
				fieldKeyName: 123, // not a string, should be ignored
				"price":      "not-a-float",
			},
			wantName:     testName,
			wantDesc:     testDescription,
			wantPrice:    testPrice,
			wantImageURL: testImageURL,
		},
		{
			name: "unknown keys are ignored",
			updates: map[string]any{
				"unknown_field": "value",
			},
			wantName:     testName,
			wantDesc:     testDescription,
			wantPrice:    testPrice,
			wantImageURL: testImageURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := New(testID, testName, testDescription, testPrice, testImageURL)
			// A fixed past timestamp makes the advance check independent of
			// clock resolution and execution speed.
			originalUpdatedDate := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
			product.UpdatedDate = originalUpdatedDate

			product.Update(tt.updates)

			if product.Name != tt.wantName {
				t.Errorf("Update() Name = %v, want %v", product.Name, tt.wantName)
			}
			if product.Description != tt.wantDesc {
				t.Errorf("Update() Description = %v, want %v", product.Description, tt.wantDesc)
			}
			if product.Price != tt.wantPrice {
				t.Errorf("Update() Price = %v, want %v", product.Price, tt.wantPrice)
			}
			if product.ImageURL != tt.wantImageURL {
				t.Errorf("Update() ImageURL = %v, want %v", product.ImageURL, tt.wantImageURL)
			}
			if !product.UpdatedDate.After(originalUpdatedDate) {
				t.Errorf("Update() UpdatedDate = %v, want after %v", product.UpdatedDate, originalUpdatedDate)
			}
		})
	}
}

func TestProductValidate(t *testing.T) {
	tests := []struct {
		name    string
		product *Product
		wantErr bool
	}{
		{
			name:    "valid product",
			product: New(testID, testName, testDescription, testPrice, testImageURL),
			wantErr: false,
		},
		{
			name:    "empty name",
			product: New(testID, "", testDescription, testPrice, testImageURL),
			wantErr: true,
		},
		{
			name:    "negative price",
			product: New(testID, testName, testDescription, -10.0, testImageURL),
			wantErr: true,
		},
		{
			name:    "zero price is valid",
			product: New(testID, testName, testDescription, 0, testImageURL),
			wantErr: false,
		},
		{
			name:    "empty image URL is valid",
			product: New(testID, testName, testDescription, testPrice, ""),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.product.Validate()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if !errors.Is(err, ErrInvalidProduct) {
					t.Errorf("Validate() error = %v, want errors.Is(ErrInvalidProduct) = true", err)
				}
				return
			}

			if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

func TestProductEntityTableName(t *testing.T) {
	entity := &ProductEntity{}
	if got := entity.TableName(); got != "products" {
		t.Errorf("TableName() = %v, want %v", got, "products")
	}
}

func TestProductEntityValidate(t *testing.T) {
	tests := []struct {
		name    string
		entity  *ProductEntity
		wantErr bool
	}{
		{
			name: "valid entity",
			entity: &ProductEntity{
				ID:    testID,
				Name:  testName,
				Price: testPrice,
			},
			wantErr: false,
		},
		{
			name: "empty name",
			entity: &ProductEntity{
				ID:    testID,
				Name:  "",
				Price: testPrice,
			},
			wantErr: true,
		},
		{
			name: "negative price",
			entity: &ProductEntity{
				ID:    testID,
				Name:  testName,
				Price: -1.0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entity.Validate()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if !errors.Is(err, ErrInvalidProduct) {
					t.Errorf("Validate() error = %v, want errors.Is(ErrInvalidProduct) = true", err)
				}
				return
			}

			if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

func TestToProductEntity(t *testing.T) {
	product := New(testID, testName, testDescription, testPrice, testImageURL)

	entity := ToProductEntity(product)

	if entity.ID != product.ID {
		t.Errorf("ToProductEntity() ID = %v, want %v", entity.ID, product.ID)
	}
	if entity.Name != product.Name {
		t.Errorf("ToProductEntity() Name = %v, want %v", entity.Name, product.Name)
	}
	if entity.Description != product.Description {
		t.Errorf("ToProductEntity() Description = %v, want %v", entity.Description, product.Description)
	}
	if entity.Price != product.Price {
		t.Errorf("ToProductEntity() Price = %v, want %v", entity.Price, product.Price)
	}
	if entity.ImageURL != product.ImageURL {
		t.Errorf("ToProductEntity() ImageURL = %v, want %v", entity.ImageURL, product.ImageURL)
	}
	if !entity.CreatedDate.Equal(product.CreatedDate) {
		t.Errorf("ToProductEntity() CreatedDate = %v, want %v", entity.CreatedDate, product.CreatedDate)
	}
	if !entity.UpdatedDate.Equal(product.UpdatedDate) {
		t.Errorf("ToProductEntity() UpdatedDate = %v, want %v", entity.UpdatedDate, product.UpdatedDate)
	}
}

func TestToProduct(t *testing.T) {
	now := time.Now().UTC()
	entity := &ProductEntity{
		ID:          testID,
		Name:        testName,
		Description: testDescription,
		Price:       testPrice,
		ImageURL:    testImageURL,
		CreatedDate: now,
		UpdatedDate: now,
	}

	product := ToProduct(entity)

	if product.ID != entity.ID {
		t.Errorf("ToProduct() ID = %v, want %v", product.ID, entity.ID)
	}
	if product.Name != entity.Name {
		t.Errorf("ToProduct() Name = %v, want %v", product.Name, entity.Name)
	}
	if product.Description != entity.Description {
		t.Errorf("ToProduct() Description = %v, want %v", product.Description, entity.Description)
	}
	if product.Price != entity.Price {
		t.Errorf("ToProduct() Price = %v, want %v", product.Price, entity.Price)
	}
	if product.ImageURL != entity.ImageURL {
		t.Errorf("ToProduct() ImageURL = %v, want %v", product.ImageURL, entity.ImageURL)
	}
	if !product.CreatedDate.Equal(entity.CreatedDate) {
		t.Errorf("ToProduct() CreatedDate = %v, want %v", product.CreatedDate, entity.CreatedDate)
	}
	if !product.UpdatedDate.Equal(entity.UpdatedDate) {
		t.Errorf("ToProduct() UpdatedDate = %v, want %v", product.UpdatedDate, entity.UpdatedDate)
	}
}

func TestToProductList(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name     string
		entities []*ProductEntity
		wantLen  int
	}{
		{
			name:     "empty list",
			entities: []*ProductEntity{},
			wantLen:  0,
		},
		{
			name:     "nil list",
			entities: nil,
			wantLen:  0,
		},
		{
			name: "multiple entities",
			entities: []*ProductEntity{
				{ID: "1", Name: "Product 1", Price: 10.0, CreatedDate: now, UpdatedDate: now},
				{ID: "2", Name: "Product 2", Price: 20.0, CreatedDate: now, UpdatedDate: now},
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := ToProductList(tt.entities)

			if len(products) != tt.wantLen {
				t.Fatalf("ToProductList() len = %v, want %v", len(products), tt.wantLen)
			}

			for i, p := range products {
				if p.ID != tt.entities[i].ID {
					t.Errorf("ToProductList()[%d].ID = %v, want %v", i, p.ID, tt.entities[i].ID)
				}
				if p.Name != tt.entities[i].Name {
					t.Errorf("ToProductList()[%d].Name = %v, want %v", i, p.Name, tt.entities[i].Name)
				}
			}
		})
	}
}
