#!/usr/bin/env bash
set -euo pipefail

gimp $(ls -1tr ~/*png | tail -n1)
