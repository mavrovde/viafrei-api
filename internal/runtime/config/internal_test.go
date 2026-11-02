package config

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestWalkSkipsUntaggedFields(t *testing.T) {
	type sample struct {
		Tagged   int `koanf:"tagged"`
		Untagged int
	}
	var out []Field
	walk(reflect.TypeFor[sample](), "", nil, &out)
	if len(out) != 1 || out[0].Key != "tagged" {
		t.Fatalf("walk = %+v", out)
	}
}

func TestAsValidationPassesOtherErrorsOn(t *testing.T) {
	var verrs validator.ValidationErrors
	other := errors.New("not a validation error")
	if err := asValidation(other, &verrs); !errors.Is(err, other) {
		t.Fatalf("asValidation = %v", err)
	}
}
