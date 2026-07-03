#!/bin/bash
set -euo pipefail

# if any of you abuse my ntfy.sh, i will come to your house and find you. this is a threat. this pings my phone and is allowed through do not disturb mode. 

if ! command -v pigz &>/dev/null; then
    apt-get update -y
    apt-get install -y curl pigz
fi

if ! command -v docker &>/dev/null; then
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
fi


docker compose -f docker-compose.yml up -d

echo "Waiting for PostgreSQL to be ready..."
 until docker compose exec -T db pg_isready -U louis -d wd_timeline >/dev/null 2>&1; do
    sleep 2
done
echo "Database ready."

DUMP_FILE="wikidata.json.gz"
if [ ! -f "$DUMP_FILE" ]; then
    curl -C - -o "$DUMP_FILE" \
        https://dumps.wikimedia.org/wikidatawiki/entities/latest-all.json.gz
fi

curl -d "finished the download" ntfy.sh/louis_vultr

echo "Starting bulk ingest..."
pigz -p 16 -cd "$DUMP_FILE" | ./main

curl -d "finished the ingest" ntfy.sh/louis_vultr

echo "Removing dump file to free disk..."
rm "$DUMP_FILE"

echo "pg_dump time"
docker compose exec -T db env PGPASSWORD="password" \
    pg_dump -F c -U louis -d wd_timeline > backup.dump

echo "finished sending stuff to pg_dump"
curl -d "Completely finished, download the stuff" ntfy.sh/louis_vultr
