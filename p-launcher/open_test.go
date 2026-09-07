package main

import "testing"

func TestDecide(t *testing.T) {
	tree := []byte(treeFixture)
	act, con, err := decide(tree, "p:m/dependabot")
	if err != nil || act != actFocus || con != 5 {
		t.Fatalf("got %v %d %v (focused is con 6, next wraps to 5)", act, con, err)
	}
	act, _, err = decide(tree, "p:m/nope")
	if err != nil || act != actLaunch {
		t.Fatalf("got %v %v", act, err)
	}
	if _, _, err := decide([]byte("{"), "x"); err == nil {
		t.Fatal("want error")
	}
}
