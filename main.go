package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/buger/jsonparser"
)

const (
	filePath = "wikidata_sample.json.gz"
)

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

func main() {
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
		lat, _ := jsonparser.GetString(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "latitude")
		lon, _ := jsonparser.GetString(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "longitude")
		locPrec, _ := jsonparser.GetInt(line, "claims", "P625", "[0]", "mainsnak", "datavalue", "value", "precision")
		if globe != "http://www.wikidata.org/entity/Q2" {
			//crazy we have to check this, wikidata is crazy
			lat = ""
			lon = ""
			locPrec = 0
		}

		timeStr, prec, err := extractWikidataTime(line, "P585")
		if err != nil {
			continue
		}

		linesRead++

		fmt.Println("Event")
		fmt.Println("Wikidata ID:", wdId)
		fmt.Println("Image URL:", imageUrl)
		fmt.Println("Location Coordinates:", lat, lon, "at precision", locPrec)
		fmt.Println("Type:", typeOf)
		fmt.Println("Title:", title)
		fmt.Println("Description:", desc)
		fmt.Println("Time:", timeStr)
		fmt.Println("Precision:", prec)
		fmt.Println("Wikipedia URL:", wikiUrl)
		fmt.Println()
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
