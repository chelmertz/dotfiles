package main

import (
	"strings"
	"testing"
)

func TestGraphqlVarsMismatch(t *testing.T) {
	q := "query=" + resolveThreadMutation
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"matching", []string{"api", "graphql", "-f", q, "-f", "id=T1"}, ""},
		{"wrong name", []string{"api", "graphql", "-f", q, "-f", "threadId=T1"}, "$id not passed"},
		{"extra field", []string{"api", "graphql", "-f", q, "-f", "id=T1", "-F", "n=1"}, "n passed"},
		{"optional may be left out", []string{"api", "graphql", "-f", "query=query($c: String) { x }"}, ""},
		{"not graphql", []string{"api", "-X", "POST", "repos/o/r/issues/1/comments", "-f", "body=x"}, ""},
	}
	for _, c := range cases {
		err := graphqlVarsMismatch(c.args)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
