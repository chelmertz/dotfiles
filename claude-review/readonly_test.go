package main

import (
	"errors"
	"strings"
	"testing"
)

// checkReadOnly refuses any gh call that could write. On REST, -f and -F
// turn `gh api` into a POST, so they are allowed for `graphql` alone, and
// there only with a query, never a mutation.
func checkReadOnly(args []string) error {
	if len(args) < 2 || args[0] != "api" {
		return errors.New("read-only gh: only `gh api` is allowed")
	}
	endpoint := ""
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") && endpoint == "" {
			endpoint = a
		}
	}
	for i, a := range args {
		switch {
		case a == "-X" || a == "--method" || strings.HasPrefix(a, "--method=") || strings.HasPrefix(a, "-X") || a == "--input" || strings.HasPrefix(a, "--input="):
			return errors.New("read-only gh: refused " + a)
		case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field":
			if endpoint != "graphql" || i+1 >= len(args) {
				return errors.New("read-only gh: fields only for a graphql query")
			}
			if v := args[i+1]; strings.HasPrefix(v, "query=") && !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(v, "query=")), "query") {
				return errors.New("read-only gh: only a graphql query, not a mutation")
			}
		case strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F") || strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field="):
			return errors.New("read-only gh: refused " + a)
		}
	}
	return nil
}

func TestCheckReadOnly(t *testing.T) {
	q := "query=" + unresolvedThreadsQuery
	allowed := [][]string{
		{"api", "repos/acme/widgets/pulls/1"},
		{"api", "--paginate", "repos/acme/widgets/pulls/1/reviews"},
		{"api", "repos/acme/widgets/compare/a...b"},
		{"api", "graphql", "-f", q, "-F", "owner=acme", "-F", "repo=widgets", "-F", "n=1"},
	}
	for _, a := range allowed {
		if err := checkReadOnly(a); err != nil {
			t.Errorf("%q refused: %v", a, err)
		}
	}
	refused := [][]string{
		{"pr", "comment", "1", "--body", "x"},
		{"pr", "review", "--approve"},
		{"api"},
		{"api", "-X", "PUT", "repos/acme/widgets/pulls/1/reviews/9/dismissals"},
		{"api", "-XPOST", "repos/acme/widgets/issues/1/comments"},
		{"api", "--method", "POST", "repos/acme/widgets/issues/1/comments"},
		{"api", "--method=PATCH", "repos/acme/widgets/pulls/1"},
		{"api", "repos/acme/widgets/issues/1/comments", "-f", "body=x"},
		{"api", "repos/acme/widgets/issues/1/comments", "-fbody=x"},
		{"api", "repos/acme/widgets/issues/1/comments", "--raw-field=body=x"},
		{"api", "repos/acme/widgets/issues/1/comments", "--input", "body.json"},
		{"api", "graphql", "-f", "query=mutation { addComment(input: {}) { clientMutationId } }"},
		{"api", "graphql", "-f", "query= mutation { x }"},
	}
	for _, a := range refused {
		if err := checkReadOnly(a); err == nil {
			t.Errorf("%q allowed", a)
		}
	}
}
