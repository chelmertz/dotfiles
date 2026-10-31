# Slow checks, run daily by .github/workflows/claude-review-slow-tests.yml
# instead of on every build. One worker keeps a mutation run's memory bounded.
# Thresholds are the measured baseline (2026-10-31: 491 killed, 0 lived,
# 87.7% of mutants covered), so a surviving mutant or a coverage drop fails.
MUTATION_EFFICACY ?= 100
MUTATION_COVERAGE ?= 87
mutate:
	CGO_ENABLED=0 go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --workers 1 --threshold-efficacy $(MUTATION_EFFICACY) --threshold-mcover $(MUTATION_COVERAGE) .

# The property tests with 10000 cases each instead of rapid's default 100.
props-long:
	CGO_ENABLED=0 go test -run Prop . -args -rapid.checks=10000

.PHONY: mutate props-long
