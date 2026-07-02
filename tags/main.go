package main

import (
	"bufio"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/jackc/pgx/v5"
)

const (
	filePath = "../wikidata_sample.json.gz"
)

func main() {
	ctx := context.Background()
	connStr := "postgres://louis:password@localhost:5432/wd_timeline?sslmode=disable"
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to open file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	var linesRead int64

	gz, err := gzip.NewReader(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create gzip reader: %v\n", err)
		os.Exit(1)
	}
	defer gz.Close()

	scanner := bufio.NewScanner(gz)
	//lines can be very big, a few mb
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*1024)
	var totalLines int64

	for scanner.Scan() {
		if linesRead >= 2000 {
			fmt.Println("2000 useful lines out of", totalLines, "lines read, stopping for now")
			break

		}
		totalLines++
		line := scanner.Bytes()
		line = []byte(strings.TrimSuffix(strings.TrimPrefix(string(line), "["), ","))

		title, err := jsonparser.GetString(line, "labels", "en", "value")
		if err != nil {
			continue
		}
		id, err := jsonparser.GetString(line, "id")
		if err != nil {
			fmt.Println("Error getting id:", err)
			continue
		}
		fmt.Println("id", id, "title", title)
		insertErr := insertData(ctx, conn, id, title)
		if insertErr != nil {
			fmt.Println("Error inserting data:", insertErr)
			continue
		}
		linesRead++
	}

}

func insertData(ctx context.Context, conn *pgx.Conn, id string, title string) error {
	_, err := conn.Exec(ctx, "UPDATE tags set NAME = $1 WHERE wikidata_qid = $2", title, id)
	if err != nil {
		return fmt.Errorf("failed to insert data: %w", err)
	}
	return nil
}
