usage:
	@echo "Available commands:"
	@grep '^[^#[:space:]].*:' Makefile

update:
	sh update-submodules.sh
