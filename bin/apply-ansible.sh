#!/bin/sh

exec ansible-playbook --verbose --connection=local --ask-become-pass ~/code/github/chelmertz/dotfiles/ansible-laptop.yml

