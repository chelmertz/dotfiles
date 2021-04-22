#!/bin/sh

exec ansible-playbook --connection=local --ask-become-pass ~/code/github/chelmertz/dotfiles/ansible-laptop.yml

