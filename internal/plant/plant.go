// Package plant holds the planted faults of #5 on a throwaway branch: a lint
// error and a failing test. It is never merged.
package plant

// unusedHelper is never called: the planted lint error (unused).
func unusedHelper() int { return 1 }
