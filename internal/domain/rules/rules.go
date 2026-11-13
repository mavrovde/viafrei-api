// Package rules is the driving-rules context: curated German driving rules
// by topic, informational and dated by their last review.
package rules

import (
	"errors"
	"time"
)

// Disclaimer is carried by every rule topic (FR-RULES-001).
const Disclaimer = "Informational, not legal advice."

// ErrUnknownTopic means no rule topic with the asked code is held.
var ErrUnknownTopic = errors.New("rules: no such topic")

// Rule is one statement of a topic.
type Rule struct {
	Text string
	// LegalBasis names the provision the rule rests on; empty when none is
	// recorded.
	LegalBasis *string
}

// Topic is a rule topic with its rules in their order.
type Topic struct {
	Code       string
	Title      string
	ReviewedOn time.Time
	Rules      []Rule
}
