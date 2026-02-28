package graph

import (
	"sync"

	"github.com/wricardo/gqlcli/example/graph/model"
)

// Resolver is the root dependency injection type for all resolvers.
// Add shared dependencies (DB, logger, etc.) here.
type Resolver struct {
	mu    sync.Mutex
	books []*model.Book
	nextID int
}

// NewResolver returns a Resolver seeded with a few example books.
func NewResolver() *Resolver {
	return &Resolver{
		nextID: 3,
		books: []*model.Book{
			{ID: "1", Title: "The Go Programming Language", Author: "Donovan & Kernighan"},
			{ID: "2", Title: "Clean Code", Author: "Robert C. Martin"},
		},
	}
}
