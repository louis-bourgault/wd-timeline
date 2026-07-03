# Wikidata downloader and indexer

```curl -s https://dumps.wikimedia.org/wikidatawiki/entities/latest-all.json.gz | zcat | head -n 30000 | gzip > wikidata_sample.json.gz```

This will download a few hundred mb of the wikidata stuff to play with for testing.

```go run main.go > test.log```

if you're doing too many, you don't wnat to be logging it all, so do something like this. This is probably about as big as you want to test locally: this took about 10 seconds on my computer and i dont see why you'd need more 
``` go run . > /dev/null```


# Killing things!
we want to delete the container every run since there's no deduplication in the db

```docker compose down -v --remove-orphans```

# New system
Removing decompression, so we just do the acutal parsing and not the gz stuff

```pigz -p 16 -cd wikidata-all.json.gz | ./main``` 
also obviously if you dont have 16 cores, don't have -p 16, this is for the massive vps im using

# AI disclosure
This repository uses quite a lot more AI than other parts of the system like the frontend, partly because parsing wikidata and lookign up all its property types and specifications is not particularly fun. Model used: mostly Mimo-M2.7 and Deepseek V4 Flash with Opencode.
This was more used for early scaffolding, not the later optimiations, which i did myself. most of the ai was nto the ai doing the work, but rather being split screened between vs code and opencode in plan mode.

# how it works

The function goes through all the lines of the gzip with either a native go reader or a pipe from pigz and for each line deetermines whether it's an event and puts it in the database. It does tagging through a set of wikidata reference types that we care about, and sees if we have what these correspond to cached already in the map. If we don't have it, it'll save it for later and populate it at the end once we know what everything is.

This runs best on an extremely beefy machine, which is expensive, in the range of a few dollars an hour.