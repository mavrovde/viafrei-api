// Command paritygather judges the shards of the store's parity set together
// (internal/adapters/store, -args -parity -parity.shard=k/n -parity.ran=FILE):
// each shard has compared its tests' named statements with the platform's
// reads and failed on a miss, but whether every named statement ran, and
// compared rows, is a question of the whole set, which no shard alone can
// answer.
//
//	go run ./internal/testkit/paritygather parity-ran-1.json parity-ran-2.json ...
//
// It exits 0 when the shards are the n shards of one set, 1..n each once,
// listing the same statements, and every statement ran in some shard and
// compared a row in some shard, unless the set accepts it unrun or with no
// row; 1 when one did not; 2 on a usage error, an unreadable file or shards
// that are not one set.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// prog prefixes every message this command prints.
const prog = "paritygather"

type statement struct {
	Name   string `json:"name"`
	Runs   int    `json:"runs"`
	Rows   int    `json:"rows"`
	Accept string `json:"accept"`
}

type shard struct {
	Shard      string      `json:"shard"`
	Statements []statement `json:"statements"`
}

var errNotOneSet = errors.New("the shards are not the shards of one set")

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, prog+": name every shard's -parity.ran file")
		return 2
	}
	shards := make([]shard, len(args))
	for i, p := range args {
		b, err := os.ReadFile(p) // #nosec G304 -- a path the caller names
		if err == nil {
			err = json.Unmarshal(b, &shards[i])
		}
		if err != nil {
			fmt.Fprintln(stderr, prog+":", err)
			return 2
		}
	}
	if err := oneSet(shards); err != nil {
		fmt.Fprintln(stderr, prog+":", err)
		return 2
	}
	bad := 0
	for i, s := range shards[0].Statements {
		runs, rows := 0, 0
		for _, sh := range shards {
			runs += sh.Statements[i].Runs
			rows += sh.Statements[i].Rows
		}
		switch {
		case strings.HasPrefix(s.Accept, "unrun"):
		case runs == 0:
			fmt.Fprintf(stderr, "parity: %s never ran beside its view statement\n", s.Name)
			bad++
		case rows == 0 && s.Accept == "":
			fmt.Fprintf(stderr, "parity: %s compared 0 rows in every run: the parity proves nothing\n", s.Name)
			bad++
		default:
			fmt.Fprintf(stdout, "parity: %s ran %d times beside its view statement, %d rows compared\n", s.Name, runs, rows)
		}
	}
	fmt.Fprintf(stdout, "%s: %d shards, %d statements, %d not proven\n", prog, len(shards), len(shards[0].Statements), bad)
	if bad > 0 {
		return 1
	}
	return 0
}

// oneSet checks that shards are the n shards of one set, each once, every
// one listing the same statements in the same order.
func oneSet(shards []shard) error {
	seen := make([]bool, len(shards))
	for _, s := range shards {
		ks, ns, _ := strings.Cut(s.Shard, "/")
		k, kerr := strconv.Atoi(ks)
		n, nerr := strconv.Atoi(ns)
		if kerr != nil || nerr != nil || n != len(shards) || k < 1 || k > n || seen[k-1] {
			return fmt.Errorf("%w: shard %q of %d files", errNotOneSet, s.Shard, len(shards))
		}
		seen[k-1] = true
		if !slices.EqualFunc(s.Statements, shards[0].Statements, func(a, b statement) bool { return a.Name == b.Name && a.Accept == b.Accept }) {
			return fmt.Errorf("%w: shard %s lists other statements than shard %s", errNotOneSet, s.Shard, shards[0].Shard)
		}
	}
	if len(shards[0].Statements) == 0 {
		return fmt.Errorf("%w: they list no statement", errNotOneSet)
	}
	return nil
}
