package main

import (
	"bufio"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/jackc/pgx/v5"
)

const (
	filePath = "wikidata_sample.json.gz"
)

var propertiesForTagging = []string{"P31", "P361", "P17", "P276", "P131", "P1346", "P710", //respectively: instance of, part of, country, location, located in the administrative territorial entity, participant in, participant
	"P530",  //diplomatic relation
	"P1534", //end cause of
	"P140",  //religion
	"P135",  //movement
	"P61",   //discoverer or inventor
	"P97",   //noble family
	"P108",  //employer - useful for nasa, east india company, etc
	"P463",  //member of
	//add more later
}

const batchSize = 10000

//QID's for annoying things we don't like
//Bullshit to do with years: 235673, Q235678, Q235680, Q235684, Q235687, Q235688, Q235690, Q217024, Q235667, Q235669, Q235671, Q235672, Q235674, Q235677.
//Other years bullshit: Q29964175, Q577, Q578, Q235670, Q29964144,

var ignoredQIDs = map[string]bool{
	"Q235673":   true,
	"Q235678":   true,
	"Q235680":   true,
	"Q235684":   true,
	"Q235687":   true,
	"Q235688":   true,
	"Q235690":   true,
	"Q217024":   true,
	"Q235667":   true,
	"Q235669":   true,
	"Q235671":   true,
	"Q235672":   true,
	"Q235674":   true,
	"Q235677":   true,
	"Q29964175": true,
	"Q577":      true,
	"Q578":      true,
	"Q235670":   true,
	"Q29964144": true,
}

type EventStruct struct {
	Title       string
	Description string
	WikiUrl     string
	YearStart   int32
	MonthStart  int32
	DayStart    int32
	Precision   int32
	IsBce       bool
	DateDisplay string
	Latitude    *float32 //pointer so htat null is fine
	Longitude   *float32
}

//go:embed schema.sql
var schemaSQL string

func main() {
	ctx := context.Background()
	connStr := "postgres://louis:password@localhost:5432/wd_timeline?sslmode=disable"
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, schemaSQL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create schema: %v\n", err)
		os.Exit(1)
	}

	eventChannel := make(chan EventStruct, 50000)

	go func() {
		defer close(eventChannel)

		file, _ := os.Open(filePath)
		defer file.Close()

		var linesRead int64

		gz, _ := gzip.NewReader(file)
		defer gz.Close()

		scanner := bufio.NewScanner(gz)
		//lines can be very big, a few mb
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024*1024)

		for scanner.Scan() {
			if linesRead >= 10 {

				break

			}
			line := scanner.Bytes()
			line = []byte(strings.TrimSuffix(strings.TrimPrefix(string(line), "["), ","))

			var typeOf string
			jsonparser.ArrayEach(line, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
				id, _ := jsonparser.GetString(value, "mainsnak", "datavalue", "value", "id")
				typeOf = id
			}, "claims", "P31")

			if ignoredQIDs[typeOf] {
				continue
			}

			wdId, err := jsonparser.GetString(line, "id")
			if err != nil {
				continue
			}

			title, err := jsonparser.GetString(line, "labels", "en", "value")
			if err != nil {
				continue
			}
			desc, _ := jsonparser.GetString(line, "descriptions", "en", "value")

			wikiUrl := ""
			wikiTitle, err := jsonparser.GetString(line, "sitelinks", "enwiki", "title")
			if err == nil && wikiTitle != "" {
				formattedTitle := strings.ReplaceAll(wikiTitle, " ", "_")
				wikiUrl = fmt.Sprintf("https://en.wikipedia.org/wiki/%s", url.PathEscape(formattedTitle))
			}
			if wikiUrl == "" {
				//we only want events significant enough to have a page
				continue
			}

			imageUrl, _ := jsonparser.GetString(line, "claims", "P18", "[0]", "mainsnak", "datavalue", "value")
			if imageUrl != "" {
				imageUrl = fmt.Sprintf("https://commons.wikimedia.org/wiki/Special:FilePath/%s", url.PathEscape(imageUrl))
			}

			//worth remembering this is WSG84
			globe, _ := jsonparser.GetString(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "globe")
			lat, _ := jsonparser.GetFloat(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "latitude")
			lon, _ := jsonparser.GetFloat(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "longitude")
			locPrec, _ := jsonparser.GetInt(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "precision")
			if globe != "http://www.wikidata.org/entity/Q2" {
				//crazy we have to check this, wikidata is crazy
				lat = 0
				lon = 0
				locPrec = 0
			}

			timeStr, prec, err := extractWikidataTime(line, "P585")
			if err != nil {
				continue
			}

			QIDs := ExtractTagQIDs(line)

			linesRead++

			fmt.Println("Event")
			fmt.Println("Wikidata ID:", wdId)
			fmt.Println("Image URL:", imageUrl)
			fmt.Println("Location Coordinates:", lat, lon, "at precision", locPrec)
			fmt.Println("QIDs:", QIDs)
			fmt.Println("Title:", title)
			fmt.Println("Description:", desc)
			fmt.Println("Time:", timeStr)
			fmt.Println("Precision:", prec)
			fmt.Println("Wikipedia URL:", wikiUrl)
			fmt.Println()

			event := EventStruct{
				Title:       title,
				Description: desc,
				WikiUrl:     wikiUrl,
				YearStart:   0,
				MonthStart:  0,
				DayStart:    0,
				Precision:   int32(prec),
				IsBce:       false,
				DateDisplay: timeStr,
				Latitude:    nil, //set to lat if locPrec is high enough
				Longitude:   nil, //set to lon if locPrec is high enough
			}
			eventChannel <- event
		}
	}()

	flushBatch(ctx, conn, eventChannel)

}

func flushBatch(ctx context.Context, conn *pgx.Conn, eventChan <-chan EventStruct) {
	var rows [][]interface{}

	for event := range eventChan {
		rows = append(rows, []interface{}{
			event.Title,
			event.Description,
			event.WikiUrl,
			event.YearStart,
			event.MonthStart,
			event.DayStart,
			event.Precision,
			event.IsBce,
			event.DateDisplay,
			event.Latitude,
			event.Longitude,
		})

		if len(rows) >= batchSize {
			executeCopy(ctx, conn, rows)
			rows = rows[:0] // Clear slice allocation memory window quickly
		}
	}

	// Flush out any remaining hanging records at EOF
	if len(rows) > 0 {
		executeCopy(ctx, conn, rows)
	}
}

func executeCopy(ctx context.Context, conn *pgx.Conn, rows [][]interface{}) {
	//this has to be the same as the order within flushBatch
	targetColumns := []string{
		"title", "description", "wiki_url",
		"year_start", "month_start", "day_start",
		"precision", "is_bce", "date_display",
		"latitude", "longitude",
	}

	_, err := conn.CopyFrom(
		ctx,
		pgx.Identifier{"events"},
		targetColumns,
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		fmt.Printf("Critical Error executing COPY pipeline operation: %v\n", err)
	}
}

func extractWikidataTime(line []byte, property string) (string, int64, error) {
	propertyArray, _, _, err := jsonparser.Get(line, "claims", property)
	if err != nil {
		return "", 0, err
	}

	var timeStr string
	var precision int64

	_, err = jsonparser.ArrayEach(propertyArray, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		t, _ := jsonparser.GetString(value, "mainsnak", "datavalue", "value", "time")
		p, _ := jsonparser.GetInt(value, "mainsnak", "datavalue", "value", "precision")
		timeStr = t
		precision = p
	})

	if timeStr == "" {
		return "", 0, fmt.Errorf("no time found for property %s", property)
	}

	return timeStr, precision, nil
}

func ExtractTagQIDs(line []byte) []string {
	var discoveredQIDs []string

	for _, prop := range propertiesForTagging {
		propertyArray, _, _, err := jsonparser.Get(line, "claims", prop)
		if err != nil {
			continue
		}
		//for each property, get teh qid of the value, and add it. This gives us things that aren't named very nicely, but we can do a second pass and name them later
		//for example, "Ich Bin ein Berliner" may have Q183 for germany, whch is obviously not very readable
		jsonparser.ArrayEach(propertyArray, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			qid, err := jsonparser.GetString(value, "mainsnak", "datavalue", "value", "id")
			if err == nil && qid != "" {
				discoveredQIDs = append(discoveredQIDs, qid)
			}
		})
	}

	return discoveredQIDs
}
