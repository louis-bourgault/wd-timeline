# Wikidata downloader and indexer

```curl -s https://dumps.wikimedia.org/wikidatawiki/entities/latest-all.json.gz | zcat | head -n 30000 | gzip > wikidata_sample.json.gz```

This will download a few hundred mb of the wikidata stuff to play with for testing.

```go run main.go > test.log```

if you're doing too many, you don't wnat to be logging it all, so do something like this. This is probably about as big as you want to test locally: this took about 10 seconds on my computer and i dont see why you'd need more 
``` go run . > /dev/null```


# Killing things!
we want to delete the container every run since there's no deduplication in the db

```docker compose down -v --remove-orphans```

# AI disclosure
This repository uses quite a lot more AI than other parts of the system like the frontend, partly because parsing wikidata and lookign up all its property types and specifications is not particularly fun. Model used: mostly Mimo-M2.7 and Deepseek V4 Flash with Opencode.