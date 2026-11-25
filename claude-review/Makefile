# Slow checks, run daily by .github/workflows/claude-review-slow-tests.yml
# instead of on every build. One worker keeps a local mutation run's memory
# bounded; CI passes WORKERS=4 to use the runner's four cores.
# The thresholds are in .gremlins.yaml: gremlins v0.6.0 ignores them as flags.
WORKERS ?= 1

mutate:
	CGO_ENABLED=0 go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --workers $(WORKERS) .

# The property tests with 10000 cases each instead of rapid's default 100.
# The two history generators take about 50 ms a case, so together they run
# past go test's default 10 min timeout.
props-long:
	CGO_ENABLED=0 go test -timeout 45m -run Prop . -args -rapid.checks=10000

.PHONY: mutate props-long
