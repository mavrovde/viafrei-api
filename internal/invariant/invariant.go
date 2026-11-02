// Package invariant names the errors the code's own preconditions rule out.
//
// A library call that returns an error only for input this code never builds
// (marshalling a value of a fixed type, opening a pool from a configuration
// that was validated) is wrapped in Must: should the precondition ever break,
// the process stops loudly instead of carrying an error branch no input can
// reach.
package invariant

import "fmt"

// Must returns v, or panics when err is not nil.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("invariant broken: %v", err))
	}
	return v
}

// Check panics when err is not nil.
func Check(err error) { Must(struct{}{}, err) }
