package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// Redacted is what every printable form of a Secret shows.
const Redacted = "<redacted>"

// Secret holds a confidential value. Its string, JSON, text and log forms are
// all Redacted, so the value cannot leak through a log line, an error message
// or a dump of the configuration (CFG-007). Use Reveal only where the value is
// handed to the component that needs it.
type Secret struct{ value string }

// NewSecret wraps a value.
func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal returns the confidential value.
func (s Secret) Reveal() string { return s.value }

// IsSet reports whether a non-empty value is held.
func (s Secret) IsSet() bool { return s.value != "" }

// String implements fmt.Stringer.
func (s Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer, so %#v is redacted too.
func (s Secret) GoString() string { return Redacted }

// Format implements fmt.Formatter, so every verb (%v, %+v, %s, %q, %x) is
// redacted, including when the Secret is printed as part of a struct.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Redacted)) }

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }

// MarshalText implements encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }
