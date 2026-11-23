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
	// ID is the statement's key (truck_ban.sunday).
	ID   string
	Text string
	// LegalBasis names the provision the rule rests on; empty when none is
	// recorded.
	LegalBasis *string
	// LegalBasisURL is where the provision is published; nil when none is
	// recorded.
	LegalBasisURL *string
	// LegalBasisNote is the note on the provision, in the asked language
	// (German when no English one is recorded); nil when none is.
	LegalBasisNote *string
	// ReviewedOn is when the statement was last read against its source.
	ReviewedOn time.Time
	// ToVerify names what of the statement is not confirmed; nil when all
	// is.
	ToVerify *string
}

// Topic is a rule topic with its rules in their order.
type Topic struct {
	Code  string
	Title string
	// ReviewedOn is the oldest review of its statements: the topic is as
	// current as its least recently read rule, as the platform states it.
	ReviewedOn time.Time
	Rules      []Rule
}
