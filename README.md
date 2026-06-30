# Wikidata downloader and indexer

```curl -s https://dumps.wikimedia.org/wikidatawiki/entities/latest-all.json.gz | zcat | head -n 30000 | gzip > wikidata_sample.json.gz```

This will download a few hundred mb of the wikidata stuff to play with for testing.

```go run main.go > test.log```