package main

import (
	"fmt"
	"regexp"
	"strings"
)

// graphqlDecl is one variable a query declares, and whether it is required.
var graphqlDecl = regexp.MustCompile(`\$(\w+)\s*:\s*[\w\[\]]+(!?)`)

// graphqlVarsMismatch checks a `gh api graphql` call: every field passed
// with -f or -F must be a variable the query declares, and every required
// variable must be passed. GitHub answers a mismatch only at runtime ("Variable
// $id of type ID! was provided invalid value"), and a fake gh accepts any
// name, so `post resolve` failed on every call while its test passed.
func graphqlVarsMismatch(args []string) error {
	if len(args) < 2 || args[0] != "api" || args[1] != "graphql" {
		return nil
	}
	query := ""
	passed := map[string]bool{}
	for i := 2; i+1 < len(args); i++ {
		if args[i] != "-f" && args[i] != "-F" {
			continue
		}
		name, value, _ := strings.Cut(args[i+1], "=")
		if name == "query" {
			query = value
		} else {
			passed[name] = true
		}
		i++
	}
	declared := map[string]bool{}
	for _, m := range graphqlDecl.FindAllStringSubmatch(query, -1) {
		declared[m[1]] = true
		if m[2] == "!" && !passed[m[1]] {
			return fmt.Errorf("graphql: required variable $%s not passed", m[1])
		}
	}
	for name := range passed {
		if !declared[name] {
			return fmt.Errorf("graphql: %s passed but the query declares no $%s", name, name)
		}
	}
	return nil
}
