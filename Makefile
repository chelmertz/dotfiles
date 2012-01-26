usage:
	@echo "Available commands:"
	@grep '^[^#[:space:]].*:' Makefile | cut -d : -f 1

update:
	git pull
	sh update-submodules.sh
