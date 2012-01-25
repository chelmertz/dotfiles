usage:
	@echo "Available commands:"
	@grep '^[^#[:space:]].*:' Makefile | cut -d : -f 1

update:
	sh update-submodules.sh
