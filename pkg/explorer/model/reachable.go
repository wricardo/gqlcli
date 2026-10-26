package model

// ReachableField pairs a root operation with a target field. maxDepth is
// resolver-private query context: the nested paths connection paginates
// independently but must inherit the outer traversal depth.
type ReachableField struct {
	Operation *Operation   `json:"operation"`
	Target    *SchemaField `json:"target"`
	maxDepth  int
}

// NewReachableField records the outer search depth without encoding it into a
// public GraphQL field or a pagination cursor.
func NewReachableField(operation *Operation, target *SchemaField, maxDepth int) *ReachableField {
	return &ReachableField{Operation: operation, Target: target, maxDepth: maxDepth}
}

func (r *ReachableField) MaxDepth() int { return r.maxDepth }
