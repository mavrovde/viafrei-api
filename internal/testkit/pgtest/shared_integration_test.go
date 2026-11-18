//go:build integration

package pgtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Each set-up step on a maintenance session that is closed, or that is not
// allowed the step, is that step's error.
func TestSetUpStepsReportTheirFaults(t *testing.T) {
	ctx := context.Background()
	closed, err := pgx.Connect(ctx, harness.urlOf(harness.OwnerURL, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close(ctx)
	steps := map[string]func(*pgx.Conn) error{
		"copySettings": func(c *pgx.Conn) error { return copySettings(ctx, c, "pgtest_none") },
		"isTemplate":   func(c *pgx.Conn) error { _, err := isTemplate(ctx, c, Database); return err },
		"seedTemplate": func(c *pgx.Conn) error { return harness.seedTemplate(ctx, c, "pgtest_none", "-- no rows\n") },
		"markTemplate": func(c *pgx.Conn) error { return markTemplate(ctx, c, Database) },
		"once": func(c *pgx.Conn) error {
			return once(ctx, c, func() (bool, error) { return false, nil }, func() error { return nil })
		},
	}
	for name, step := range steps {
		if err := step(closed); err == nil {
			t.Errorf("%s on a closed session succeeded", name)
		}
	}

	// The reader may read the catalogue but neither create, drop, alter nor
	// copy a setting.
	owner, err := pgx.Connect(ctx, harness.urlOf(harness.OwnerURL, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	if _, err := owner.Exec(ctx, "create database pgtest_owned_b"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "drop database if exists pgtest_owned_b") })
	reader, err := pgx.Connect(ctx, harness.urlOf(harness.ReaderURL, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(ctx) }()
	for name, step := range map[string]func(*pgx.Conn) error{
		"copySettings":       func(c *pgx.Conn) error { return copySettings(ctx, c, "pgtest_none") },
		"seedTemplate drop":  func(c *pgx.Conn) error { return harness.seedTemplate(ctx, c, "pgtest_owned", "-- no rows\n") },
		"seedTemplate build": func(c *pgx.Conn) error { return harness.seedTemplate(ctx, c, "pgtest_none", "-- no rows\n") },
		"markTemplate":       func(c *pgx.Conn) error { return markTemplate(ctx, c, Database) },
	} {
		if err := step(reader); err == nil {
			t.Errorf("%s as the reader succeeded", name)
		}
	}
}
