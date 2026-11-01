package config

import (
	"reflect"
	"strings"
	"time"
)

// Field describes one configuration key, reflected from the Config struct.
type Field struct {
	Key      string // dotted key path, for example "db.pool_max"
	Env      string // environment spelling, for example "VFAPI_DB_POOL_MAX"
	Type     string // duration, int, string, bool, list or secret
	Default  string // as written in the declaration
	Validate string // the raw validate tag: the bounds
	Secret   bool
	Desc     string
	index    []int
}

var (
	durationType = reflect.TypeFor[time.Duration]()
	secretType   = reflect.TypeFor[Secret]()
)

// Fields returns every key of the declaration in declaration order.
func Fields() []Field {
	var out []Field
	walk(reflect.TypeFor[Config](), "", nil, &out)
	return out
}

// FieldByKey returns the field declared for key.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Fields() {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// EnvName returns the environment variable spelling of a key.
func EnvName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func walk(t reflect.Type, prefix string, index []int, out *[]Field) {
	for i := range t.NumField() {
		sf := t.Field(i)
		name := sf.Tag.Get("koanf")
		if name == "" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		idx := append(append([]int(nil), index...), i)
		if sf.Type.Kind() == reflect.Struct && sf.Type != secretType {
			walk(sf.Type, key, idx, out)
			continue
		}
		*out = append(*out, Field{
			Key:      key,
			Env:      EnvName(key),
			Type:     typeName(sf.Type),
			Default:  sf.Tag.Get("default"),
			Validate: sf.Tag.Get("validate"),
			Secret:   sf.Tag.Get("secret") == "true",
			Desc:     sf.Tag.Get("desc"),
			index:    idx,
		})
	}
}

func typeName(t reflect.Type) string {
	switch {
	case t == secretType:
		return "secret"
	case t == durationType:
		return "duration"
	case t.Kind() == reflect.Int:
		return "int"
	case t.Kind() == reflect.Bool:
		return "bool"
	case t.Kind() == reflect.Slice:
		return "list"
	default:
		return "string"
	}
}
