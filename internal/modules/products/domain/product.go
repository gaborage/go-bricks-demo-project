package domain

import (
	"fmt"
	"time"
)

var (
	ErrInvalidProduct = fmt.Errorf("invalid product data")
)

type Product struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Price       float64   `json:"price"`
	ImageURL    string    `json:"imageURL"`
	CreatedDate time.Time `json:"createdDate"`
	UpdatedDate time.Time `json:"updatedDate"`
}

func New(id, name, description string, price float64, imageURL string) *Product {
	timestamp := time.Now().UTC()
	return &Product{
		ID:          id,
		Name:        name,
		Description: description,
		Price:       price,
		ImageURL:    imageURL,
		CreatedDate: timestamp,
		UpdatedDate: timestamp,
	}
}

func (p *Product) Update(updates map[string]any) {
	if name, ok := updates["name"].(string); ok {
		p.Name = name
	}
	if description, ok := updates["description"].(string); ok {
		p.Description = description
	}
	if price, ok := updates["price"].(float64); ok {
		p.Price = price
	}
	if imageURL, ok := updates["image_url"].(string); ok {
		p.ImageURL = imageURL
	}
	p.UpdatedDate = time.Now().UTC()
}

func (p *Product) Validate() error {
	if p.Name == "" {
		return ErrInvalidProduct
	}
	if p.Price < 0 {
		return ErrInvalidProduct
	}
	return nil
}

type ProductEntity struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	Price       float64   `json:"price" db:"price"`
	ImageURL    string    `json:"imageURL" db:"image_url"`
	CreatedDate time.Time `json:"createdDate" db:"created_date"`
	UpdatedDate time.Time `json:"updatedDate" db:"updated_date"`
}

func (p *ProductEntity) TableName() string {
	return "products"
}

func (p *ProductEntity) Validate() error {
	if p.Name == "" {
		return ErrInvalidProduct
	}
	if p.Price < 0 {
		return ErrInvalidProduct
	}
	return nil
}

func ToProductEntity(p *Product) *ProductEntity {
	return &ProductEntity{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Price:       p.Price,
		ImageURL:    p.ImageURL,
		CreatedDate: p.CreatedDate,
		UpdatedDate: p.UpdatedDate,
	}
}

func ToProduct(pe *ProductEntity) *Product {
	return &Product{
		ID:          pe.ID,
		Name:        pe.Name,
		Description: pe.Description,
		Price:       pe.Price,
		ImageURL:    pe.ImageURL,
		CreatedDate: pe.CreatedDate,
		UpdatedDate: pe.UpdatedDate,
	}
}

func ToProductList(entities []*ProductEntity) []*Product {
	products := make([]*Product, len(entities))
	for i, e := range entities {
		products[i] = ToProduct(e)
	}
	return products
}
