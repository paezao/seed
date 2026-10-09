package models

import "context"

// Purpose says what a model call is for, so its cost can be told apart
// (my owner's chat, an evolution, a routine, my doctor…).
type Purpose struct {
	Kind string // chat, evolution, routine, health, other
	Ref  string // e.g. the evolution's or routine's id
}

type purposeKey struct{}

// WithPurpose marks the model calls made with ctx.
func WithPurpose(ctx context.Context, p Purpose) context.Context {
	return context.WithValue(ctx, purposeKey{}, p)
}

// PurposeOf returns what calls made with ctx are for.
func PurposeOf(ctx context.Context) Purpose {
	if p, ok := ctx.Value(purposeKey{}).(Purpose); ok {
		return p
	}
	return Purpose{Kind: "other"}
}
