#!/bin/bash
set -e

if [ ! -f .env ]; then
    echo "No .env file found. Copy .env.example to .env and fill in values."
    exit 1
fi

export $(grep -v '^#' .env | xargs)
mkdir -p "$(dirname "${DB_PATH:-./data/mempalace.db}")"
go run ./cmd/server
