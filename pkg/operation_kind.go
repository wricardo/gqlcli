package gqlcli

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// GraphQL operation kinds, as reported by DocumentOperationKind and carried in
// RequestInfo.Type.
const (
	OperationQuery        = "query"
	OperationMutation     = "mutation"
	OperationSubscription = "subscription"
)

// DocumentOperationKind reports what a GraphQL document actually does, by
// parsing it. A document holding several operations is reported by its most
// privileged one: any mutation makes the whole document a mutation.
//
// Parsing matters when the document is untrusted — searching the text for the
// word "mutation" is wrong in both directions. It matches the word inside a
// string literal or comment, and it misses a document that never spells the
// keyword: a bare `{ ... }` is a query, and a mutation can arrive via a helper
// named for something else.
func DocumentOperationKind(document string) (string, error) {
	kinds, err := documentOperationKinds(document)
	if err != nil {
		return "", err
	}
	kind := OperationQuery
	for _, k := range kinds {
		switch k {
		case OperationMutation:
			return OperationMutation, nil
		case OperationSubscription:
			kind = OperationSubscription
		}
	}
	return kind, nil
}

// RequireOperationKind returns an error unless every operation in document is
// of the wanted kind. Use it to enforce that a tool advertised as read-only
// cannot be handed a mutation.
func RequireOperationKind(document, want string) error {
	kinds, err := documentOperationKinds(document)
	if err != nil {
		return err
	}
	for _, kind := range kinds {
		if kind != want {
			return fmt.Errorf("expected a %s document, but it contains a %s operation", want, kind)
		}
	}
	return nil
}

func documentOperationKinds(document string) ([]string, error) {
	if strings.TrimSpace(document) == "" {
		return nil, fmt.Errorf("document is empty")
	}
	parsed, err := parser.ParseQuery(&ast.Source{Name: "operation", Input: document})
	if err != nil {
		return nil, fmt.Errorf("invalid GraphQL document: %w", err)
	}
	if len(parsed.Operations) == 0 {
		return nil, fmt.Errorf("document contains no operation")
	}
	kinds := make([]string, 0, len(parsed.Operations))
	for _, op := range parsed.Operations {
		switch op.Operation {
		case ast.Mutation:
			kinds = append(kinds, OperationMutation)
		case ast.Subscription:
			kinds = append(kinds, OperationSubscription)
		default:
			// The shorthand `{ ... }` carries no keyword and is a query.
			kinds = append(kinds, OperationQuery)
		}
	}
	return kinds, nil
}
