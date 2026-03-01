package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/wricardo/gqlcli/example/graph/model"
)

type storeData struct {
	Books   []*model.Book   `json:"books"`
	Authors []*model.Author `json:"authors"`
	NextID  int             `json:"nextId"`
}

// Resolver is the root dependency injection type for all resolvers.
type Resolver struct {
	mu        sync.Mutex
	books     []*model.Book
	authors   []*model.Author
	nextID    int
	storePath string
}

// NewResolver returns a Resolver that persists to store.json.
func NewResolver() *Resolver {
	r := &Resolver{
		storePath: "store.json",
		nextID:    1,
		books:     []*model.Book{},
		authors:   []*model.Author{},
	}
	r.load()
	return r
}

func (r *Resolver) load() {
	data, err := os.ReadFile(r.storePath)
	if err != nil {
		return // File doesn't exist yet
	}
	var s storeData
	if json.Unmarshal(data, &s) == nil {
		r.books = s.Books
		r.authors = s.Authors
		r.nextID = s.NextID
	}
}

func (r *Resolver) save() error {
	data := storeData{Books: r.books, Authors: r.authors, NextID: r.nextID}
	bytes, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(r.storePath, bytes, 0644)
}

func (r *Resolver) getOrCreateAuthor(name string) *model.Author {
	for _, a := range r.authors {
		if a.Name == name {
			return a
		}
	}
	author := &model.Author{
		ID:   fmt.Sprintf("a%d", r.nextID),
		Name: name,
	}
	r.authors = append(r.authors, author)
	return author
}
