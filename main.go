package main

import (
	"bufio"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strconv"
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

type TagRecord struct {
	Name     string
	Property string
	QID      string
}

type EventStruct struct {
	Title          string
	Description    string
	WikiUrl        string
	ImageURL       string
	YearStart      int32
	MonthStart     int32
	DayStart       int32
	Precision      int32
	IsBce          bool
	DateDisplay    string
	YearEnd        int32
	MonthEnd       int32
	DayEnd         int32
	EndIsBce       bool
	EndDateDisplay string
	EndPrecision   int32
	Latitude       *float32
	Longitude      *float32
	TagRecords     []TagRecord
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
			if linesRead >= 100 {
				fmt.Println("100 useful lines out of", totalLines, "lines read, stopping for now")
				break

			}
			totalLines++
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

			_, err := jsonparser.GetString(line, "id")
			//we should save this somewhere, but for now we don't need it
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
				timeStr, prec, err = extractWikidataTime(line, "P580")
			}
			if err != nil {
				timeStr, prec, err = extractWikidataTime(line, "P571")
			}
			if err != nil {
				continue
			}

			year, month, day, isBce, err := parseWikidataTimeString(timeStr)
			if err != nil {
				continue
			}

			//go down in priority: P582 (end time), P576 (dissolved, abolished or demolished date), P2669 (date of death)
			//for example, the "fall of the Berlin Wall" has P582, but "Berlin Wall" has P576, and "John F. Kennedy" has P2669
			endTimeStr, endPrec, endErr := extractWikidataTime(line, "P582")
			if endErr != nil {
				endTimeStr, endPrec, endErr = extractWikidataTime(line, "P576")
			}
			if endErr != nil {
				endTimeStr, endPrec, endErr = extractWikidataTime(line, "P2669")
			}
			var endYear, endMonth, endDay int32
			var endIsBce bool
			if endErr == nil {
				ey, em, ed, eb, err := parseWikidataTimeString(endTimeStr)
				if err == nil {
					endYear = int32(ey)
					endMonth = int32(em)
					endDay = int32(ed)
					endIsBce = eb
				}
			}

			tagRecords := ExtractTagRecords(line)
			seen := make(map[TagRecord]struct{})
			uniqueTagRecords := make([]TagRecord, 0, len(tagRecords))
			for _, tr := range tagRecords {
				if _, ok := seen[tr]; !ok {
					seen[tr] = struct{}{}
					uniqueTagRecords = append(uniqueTagRecords, tr)
				}
			}
			tagRecords = uniqueTagRecords

			// fmt.Println("QIDs:", QIDs)

			var latitude *float32
			var longitude *float32
			if locPrec > 0 {
				lat32 := float32(lat)
				lon32 := float32(lon)
				latitude = &lat32
				longitude = &lon32
			}

			linesRead++

			// fmt.Println("Event")
			// fmt.Println("Wikidata ID:", wdId)
			// fmt.Println("Image URL:", imageUrl)
			// fmt.Println("Location Coordinates:", lat, lon, "at precision", locPrec)
			// fmt.Println("QIDs:", QIDs)
			// fmt.Println("Title:", title)
			// fmt.Println("Description:", desc)
			// fmt.Println("Time:", timeStr)
			// fmt.Println("Precision:", prec)
			// fmt.Println("Wikipedia URL:", wikiUrl)
			// fmt.Println()

			event := EventStruct{
				Title:          title,
				Description:    desc,
				WikiUrl:        wikiUrl,
				ImageURL:       imageUrl,
				YearStart:      int32(year),
				MonthStart:     int32(month),
				DayStart:       int32(day),
				Precision:      int32(prec),
				IsBce:          isBce,
				DateDisplay:    formatWikidataDate(year, month, day, isBce, int32(prec)),
				YearEnd:        endYear,
				MonthEnd:       endMonth,
				DayEnd:         endDay,
				EndIsBce:       endIsBce,
				EndDateDisplay: formatWikidataDate(int(endYear), int(endMonth), int(endDay), endIsBce, int32(endPrec)),
				EndPrecision:   int32(endPrec),
				Latitude:       latitude,
				Longitude:      longitude,
				TagRecords:     tagRecords,
			}
			eventChannel <- event
		}
	}()

	flushBatch(ctx, conn, eventChannel)

}

func flushBatch(ctx context.Context, conn *pgx.Conn, eventChan <-chan EventStruct) {
	var rows [][]interface{}
	var tagRecordsList [][]TagRecord
	var wikiURLs []string

	for event := range eventChan {
		rows = append(rows, []interface{}{
			event.Title,
			event.Description,
			event.WikiUrl,
			event.ImageURL,
			event.YearStart,
			event.MonthStart,
			event.DayStart,
			event.YearEnd,
			event.MonthEnd,
			event.DayEnd,
			event.Precision,
			event.IsBce,
			event.DateDisplay,
			event.EndDateDisplay,
			event.EndIsBce,
			event.Latitude,
			event.Longitude,
		})
		tagRecordsList = append(tagRecordsList, event.TagRecords)
		wikiURLs = append(wikiURLs, event.WikiUrl)

		if len(rows) >= batchSize {
			flushBatchData(ctx, conn, rows, tagRecordsList, wikiURLs)
			rows = rows[:0]
			tagRecordsList = tagRecordsList[:0]
			wikiURLs = wikiURLs[:0]
		}
	}

	if len(rows) > 0 {
		flushBatchData(ctx, conn, rows, tagRecordsList, wikiURLs)
	}
}

func flushBatchData(ctx context.Context, conn *pgx.Conn, rows [][]interface{}, tagRecordsList [][]TagRecord, wikiURLs []string) {
	uniqueTags := make(map[TagRecord]struct{})
	for _, tagRecords := range tagRecordsList {
		for _, tr := range tagRecords {
			uniqueTags[tr] = struct{}{}
		}
	}

	for tr := range uniqueTags {
		_, err := conn.Exec(ctx,
			"INSERT INTO tags (name, wikidata_qid) VALUES (NULL, $1) ON CONFLICT (wikidata_qid) DO NOTHING",
			tr.QID)
		if err != nil {
			fmt.Printf("Error inserting tag %s (%s): %v\n", tr.QID, tr.Property, err)
		}
	}

	executeCopy(ctx, conn, rows)

	if len(uniqueTags) == 0 {
		return
	}

	eventIDs := make(map[string]int64, len(wikiURLs))
	for _, url := range wikiURLs {
		var id int64
		err := conn.QueryRow(ctx, "SELECT id FROM events WHERE wiki_url = $1", url).Scan(&id)
		if err == nil {
			eventIDs[url] = id
		}
	}

	tagIDs := make(map[string]int64, len(uniqueTags))
	for tr := range uniqueTags {
		var id int64
		err := conn.QueryRow(ctx, "SELECT id FROM tags WHERE wikidata_qid = $1", tr.QID).Scan(&id)
		if err == nil {
			tagIDs[tr.QID] = id
		}
	}

	var etRows [][]interface{}
	for i, tagRecords := range tagRecordsList {
		eventID, ok := eventIDs[wikiURLs[i]]
		if !ok {
			continue
		}
		for _, tr := range tagRecords {
			tagID, ok := tagIDs[tr.QID]
			if !ok {
				continue
			}
			etRows = append(etRows, []interface{}{eventID, tagID, tr.Property})
		}
	}

	if len(etRows) > 0 {
		_, err := conn.CopyFrom(ctx,
			pgx.Identifier{"event_tags"},
			[]string{"event_id", "tag_id", "wikidata_property"},
			pgx.CopyFromRows(etRows))
		if err != nil {
			fmt.Printf("Error inserting event_tags: %v\n", err)
		}
	}
}

func executeCopy(ctx context.Context, conn *pgx.Conn, rows [][]interface{}) {
	targetColumns := []string{
		"title", "description", "wiki_url", "image_url",
		"year_start", "month_start", "day_start",
		"year_end", "month_end", "day_end",
		"precision", "is_bce", "date_display",
		"end_date_display", "is_end_bce",
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

func parseWikidataTimeString(timeStr string) (year, month, day int, isBce bool, err error) {
	if len(timeStr) < 11 {
		return 0, 0, 0, false, fmt.Errorf("invalid time string: %s", timeStr)
	}

	isBce = timeStr[0] == '-'

	datePart := timeStr[1:]
	idx := strings.Index(datePart, "-")
	if idx <= 0 {
		return 0, 0, 0, false, fmt.Errorf("cannot parse year from: %s", timeStr)
	}

	year, err = strconv.Atoi(datePart[:idx])
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("invalid year in %s: %v", timeStr, err)
	}

	rest := datePart[idx+1:]
	if len(rest) >= 2 {
		month, _ = strconv.Atoi(rest[:2])
	}
	if len(rest) >= 5 {
		day, _ = strconv.Atoi(rest[3:5])
	}

	return year, month, day, isBce, nil
}

func formatWikidataDate(year, month, day int, isBce bool, precision int32) string {
	if year == 0 {
		return ""
	}

	var parts []string

	// Wikidata precision: 9=year, 10=month, 11=day
	if precision >= 11 && day > 0 {
		parts = append(parts, fmt.Sprintf("%02d", day))
	}
	if precision >= 10 && month > 0 {
		monthNames := []string{"", "January", "February", "March", "April", "May", "June",
			"July", "August", "September", "October", "November", "December"}
		parts = append(parts, monthNames[month])
	}
	// Year always shown
	parts = append(parts, fmt.Sprintf("%d", year))

	result := strings.Join(parts, " ")
	if isBce {
		result += " BCE"
	}
	return result
}

func ExtractTagRecords(line []byte) []TagRecord {
	var tagRecords []TagRecord

	for _, prop := range propertiesForTagging {
		propertyArray, _, _, err := jsonparser.Get(line, "claims", prop)
		if err != nil {
			continue
		}
		jsonparser.ArrayEach(propertyArray, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			qid, err := jsonparser.GetString(value, "mainsnak", "datavalue", "value", "id")
			if err == nil && qid != "" {
				tagRecords = append(tagRecords, TagRecord{Name: qid, Property: prop, QID: qid})
			}
		})
	}

	return tagRecords
}
