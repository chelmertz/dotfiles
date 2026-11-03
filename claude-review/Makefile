# Slow checks, run daily by .github/workflows/claude-review-slow-tests.yml
# instead of on every build. One worker keeps a mutation run's memory bounded.
# The thresholds are in .gremlins.yaml: gremlins v0.6.0 ignores them as flags.
mutate:
	CGO_ENABLED=0 go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --workers 1 .

# The property tests with 10000 cases each instead of rapid's default 100.
props-long:
	CGO_ENABLED=0 go test -run Prop . -args -rapid.checks=10000

.PHONY: mutate props-long
