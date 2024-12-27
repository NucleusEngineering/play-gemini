package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"runtime"
	"strconv"

	"strings"
	"time"

	"golang.org/x/net/context"
)

const (
	playStoreBaseURL = "https://play.google.com"
	maxRetries       = 3
	rateLimitDelay   = 5 * time.Second
)

var (
	notNumberRegex   = regexp.MustCompile(`\D`)
	scriptRegex      = regexp.MustCompile(`AF_initDataCallback(.*?)</script`)
	keyRegex         = regexp.MustCompile(`(ds:.*?)'`)
	valueRegex       = regexp.MustCompile(`data:(\[.*?\]), sideChannel: {}}\);`)
	reviewsRegex     = regexp.MustCompile(`\)]}'\n\n([\s\S]+)`)
	permissionsRegex = regexp.MustCompile(`\)]}'\n\n([\s\S]+)`)

	appDetailsURLFormat = playStoreBaseURL + "/store/apps/details?id=%s&hl=%s&gl=%s"
	reviewsURLFormat    = playStoreBaseURL + "/_/PlayStoreUi/data/batchexecute?hl=%s&gl=%s"

	permissionsURLFormat = playStoreBaseURL + "/_/PlayStoreUi/data/batchexecute?hl=%s&gl=%s"

	searchURLFormat = playStoreBaseURL + "/store/search?q=%s&c=apps&hl=%s&gl=%s"
)

type Sort int

const (
	MostRelevant Sort = 1
	Newest       Sort = 2
	Rating       Sort = 3
)

type Device int

const (
	Mobile     Device = 2
	Tablet     Device = 3
	Chromebook Device = 5
	TV         Device = 6
)

type ContinuationToken struct {
	Token             string
	Lang              string
	Country           string
	Sort              Sort
	Count             int
	FilterScoreWith   *int
	FilterDeviceWith  *int
	MaxCountEachFetch int
}

func get(ctx context.Context, url string) ([]byte, error) {

	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("Failed to fetch url %v: %v", url, err)
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		fmt.Printf("Failed to read response body: %v", err)
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("app not found (404)")
	} else if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("http error %d", resp.StatusCode)
	}

	return body, nil
}

func post(ctx context.Context, url string, data url.Values, headers map[string]string) ([]byte, error) {
	client := &http.Client{}

	req, err := http.NewRequest("POST", url, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Failed to make POST request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed to read response body: %v", err)
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("app not found (404)")
	} else if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("http error %d", resp.StatusCode)
	}

	return body, nil
}

func fetchReviewItems(ctx context.Context, fetchUrl, appID string, sort Sort, count int, filterScoreWith, filterDeviceWith *int, paginationToken string) ([]interface{}, string, error) {

	data := url.Values{}

	payloadFormat := ""

	if paginationToken != "" {
		payloadFormat = "f.req=%5B%5B%5B%22oCPfdb%22%2C%22%5Bnull%2C%5B2%2C" + strconv.Itoa(int(sort)) + "%2C%5B" + strconv.Itoa(count) + "%2Cnull%2C%5C%22" + paginationToken + "%5C%22%5D%2Cnull%2C%5Bnull%2C"
		if filterScoreWith != nil {
			payloadFormat += strconv.Itoa(*filterScoreWith)
		} else {
			payloadFormat += "null"
		}

		payloadFormat += "%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2C"
		if filterDeviceWith != nil {
			payloadFormat += strconv.Itoa(int(*filterDeviceWith))
		} else {
			payloadFormat += "null"

		}
		payloadFormat += "%5D%5D%2C%5B%5C%22" + appID + "%5C%22%2C7%5D%5D%22%2Cnull%2C%22generic%22%5D%5D%5D"

	} else {
		payloadFormat = "f.req=%5B%5B%5B%22oCPfdb%22%2C%22%5Bnull%2C%5B2%2C" + strconv.Itoa(int(sort)) + "%2C%5B" + strconv.Itoa(count) + "%5D%2Cnull%2C%5Bnull%2C"
		if filterScoreWith != nil {
			payloadFormat += strconv.Itoa(*filterScoreWith)
		} else {
			payloadFormat += "null"
		}
		payloadFormat += "%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2C"
		if filterDeviceWith != nil {
			payloadFormat += strconv.Itoa(int(*filterDeviceWith))
		} else {
			payloadFormat += "null"
		}
		payloadFormat += "%5D%5D%2C%5B%5C%22" + appID + "%5C%22%2C7%5D%5D%22%2Cnull%2C%22generic%22%5D%5D%5D"
	}

	fmt.Println(payloadFormat)

	data.Set("", payloadFormat)

	body, err := post(ctx, fetchUrl, data, map[string]string{"content-type": "application/x-www-form-urlencoded"})
	if err != nil {
		return nil, "", err
	}

	match := reviewsRegex.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return nil, "", errors.New("reviews regex did not match")

	}

	var response []interface{}
	err = json.Unmarshal([]byte(match[1]), &response)
	if err != nil {
		return nil, "", err
	}

	if len(response) == 0 || len(response[0].([]interface{})) == 0 {
		return nil, "", nil
	}
	var token string
	if len(response[0].([]interface{})) >= 4 {
		t := response[0].([]interface{})[0].([]interface{})[2].([]interface{})
		if len(t) >= 3 {
			token = t[len(t)-2].([]interface{})[0].(string)
		}
	}

	reviewItems := response[0].([]interface{})

	return reviewItems, token, nil

}

func reviews(ctx context.Context, appID, lang, country string, sort Sort, count int, filterScoreWith, filterDeviceWith *int, continuationToken *ContinuationToken) ([]map[string]interface{}, *ContinuationToken, error) {

	_ct := ""

	if continuationToken != nil {
		if continuationToken.Token == "" {
			return []map[string]interface{}{}, continuationToken, nil
		}
		lang = continuationToken.Lang
		country = continuationToken.Country
		sort = continuationToken.Sort
		count = continuationToken.Count
		filterScoreWith = continuationToken.FilterScoreWith
		filterDeviceWith = continuationToken.FilterDeviceWith
		_ct = continuationToken.Token
	} else {
		_ct = ""
	}

	url := fmt.Sprintf(reviewsURLFormat, lang, country)
	fetchCount := count
	maxCountEachFetch := 4500
	if continuationToken != nil {
		maxCountEachFetch = continuationToken.MaxCountEachFetch

	}
	result := []map[string]interface{}{}

	for fetchCount > 0 {
		if fetchCount > maxCountEachFetch {
			fetchCount = maxCountEachFetch
		}

		reviewItems, token, err := fetchReviewItems(ctx, url, appID, sort, fetchCount, filterScoreWith, filterDeviceWith, _ct)
		if err != nil {
			fmt.Printf("Error fetching reviews: %v", err)

			return nil, nil, err
		}

		for _, review := range reviewItems {

			reviewMap := make(map[int]interface{})
			for k, v := range review.([]interface{}) {
				reviewMap[k] = v
			}
			reviewResult := map[string]interface{}{}
			for k, spec := range ElementSpecs["Review"].(map[string]*ElementSpec) {
				content := spec.ExtractContent(reviewMap)

				if content != nil {
					reviewResult[k] = content

				}

			}
			result = append(result, reviewResult)
		}

		fetchCount = count - len(result)

		if token == "" {
			break
		}
		if continuationToken == nil {

			continuationToken = &ContinuationToken{
				Token:             token,
				Lang:              lang,
				Country:           country,
				Sort:              sort,
				Count:             count,
				FilterScoreWith:   filterScoreWith,
				FilterDeviceWith:  filterDeviceWith,
				MaxCountEachFetch: 4500,
			}
		} else {
			continuationToken.Token = token

		}

	}

	return result, continuationToken, nil
}

func unescapeText(s interface{}) interface{} {
	return html.UnescapeString(strings.ReplaceAll(s.(string), "<br>", "\r\n"))
}

func nestedLookup(source map[int]interface{}, indexes []int) (interface{}, error) {
	if len(indexes) == 0 {
		return nil, errors.New("indexes cannot be empty")
	}

	current, exists := source[indexes[0]]
	if !exists {
		return nil, errors.New("key not found")
	}

	if len(indexes) == 1 {
		return current, nil
	}

	nextSource := make(map[int]interface{})
	for i := 0; i < len(current.([]interface{})); i++ {
		nextSource[i] = current.([]interface{})[i]
	}

	return nestedLookup(nextSource, indexes[1:])
}

type ElementSpec struct {
	DsNum         *int
	DataMap       []int
	PostProcessor func(interface{}) interface{}
	FallbackValue interface{}
}

func NewElementSpec(dsNum *int, dataMap []int, postProcessor func(interface{}) interface{}, fallbackValue interface{}) *ElementSpec {
	return &ElementSpec{
		DsNum:         dsNum,
		DataMap:       dataMap,
		PostProcessor: postProcessor,
		FallbackValue: fallbackValue,
	}
}

func toInt(val interface{}) int {
	if v, ok := val.(float64); ok {
		return int(v)
	}
	return 0
}

func ptr(i int) *int {
	return &i
}

func processHistogram(container interface{}) interface{} {
	if data, ok := container.([]interface{}); ok && len(data) > 5 {
		return []int{
			toInt(data[1]),
			toInt(data[2]),
			toInt(data[3]),
			toInt(data[4]),
			toInt(data[5]),
		}
	}
	return []int{0, 0, 0, 0, 0}
}

func processPrice(price interface{}) interface{} {
	if p, ok := price.(float64); ok {
		return p / 1000000
	}
	return 0
}

func processFreeFlag(flag interface{}) interface{} {
	return flag == 0
}

func datetimeFromTimestamp(val interface{}) any {
	ts, ok := val.(float64)
	if !ok {
		return ""
	}
	return time.Unix(int64(ts), 0).Format(time.RFC3339)
}

func (e *ElementSpec) ExtractContent(source map[int]interface{}) interface{} {
	var result interface{}
	var err error

	defer func() {
		if r := recover(); r != nil {
			result = e.FallbackValue
		}
	}()

	if e.DsNum == nil {
		result, err = nestedLookup(source, e.DataMap)
	} else {
		dsSource, exists := source[*e.DsNum]

		if !exists {
			return e.FallbackValue
		}

		dsMap := make(map[int]interface{})
		for i := 0; i < len(dsSource.([]interface{})); i++ {
			dsMap[i] = dsSource.([]interface{})[i]
		}

		result, err = nestedLookup(dsMap, e.DataMap)
	}

	if err != nil {
		if fallbackSpec, ok := e.FallbackValue.(*ElementSpec); ok {
			return fallbackSpec.ExtractContent(source)
		}
		return e.FallbackValue
	}

	if e.PostProcessor != nil {
		fmt.Printf("Processor %s\n", runtime.FuncForPC(reflect.ValueOf(e.PostProcessor).Pointer()).Name())
		result = e.PostProcessor(result)
	}

	return result
}

// ElementSpecs equivalent in Go
var ElementSpecs = map[string]interface{}{
	"Detail": map[string]*ElementSpec{
		"title":             NewElementSpec(ptr(5), []int{1, 2, 0, 0}, nil, nil),
		"description":       NewElementSpec(ptr(5), []int{1, 2, 72, 0, 1}, unescapeText, nil),
		"descriptionHTML":   NewElementSpec(ptr(5), []int{1, 2, 72, 0, 1}, nil, nil),
		"summary":           NewElementSpec(ptr(5), []int{1, 2, 73, 0, 1}, unescapeText, nil),
		"installs":          NewElementSpec(ptr(5), []int{1, 2, 13, 0}, nil, nil),
		"minInstalls":       NewElementSpec(ptr(5), []int{1, 2, 13, 1}, nil, nil),
		"realInstalls":      NewElementSpec(ptr(5), []int{1, 2, 13, 2}, nil, nil),
		"score":             NewElementSpec(ptr(5), []int{1, 2, 51, 0, 1}, nil, nil),
		"ratings":           NewElementSpec(ptr(5), []int{1, 2, 51, 2, 1}, nil, nil),
		"reviews":           NewElementSpec(ptr(5), []int{1, 2, 51, 3, 1}, nil, nil),
		"histogram":         NewElementSpec(ptr(5), []int{1, 2, 51, 1}, processHistogram, []int{0, 0, 0, 0, 0}),
		"price":             NewElementSpec(ptr(5), []int{1, 2, 57, 0, 0, 0, 0, 1, 0, 0}, processPrice, nil),
		"free":              NewElementSpec(ptr(5), []int{1, 2, 57, 0, 0, 0, 0, 1, 0, 0}, processFreeFlag, nil),
		"contentRating":     NewElementSpec(ptr(5), []int{1, 2, 9, 0}, nil, nil),
		"contentRatingDesc": NewElementSpec(ptr(5), []int{1, 2, 9, 2, 1}, nil, nil),
	},
	"Review": map[string]*ElementSpec{
		"reviewId":      NewElementSpec(nil, []int{0}, nil, nil),
		"userName":      NewElementSpec(nil, []int{1, 0}, nil, nil),
		"userImage":     NewElementSpec(nil, []int{1, 1, 3, 2}, nil, nil),
		"content":       NewElementSpec(nil, []int{4}, nil, nil),
		"score":         NewElementSpec(nil, []int{2}, nil, nil),
		"thumbsUpCount": NewElementSpec(nil, []int{6}, nil, nil),
		"at":            NewElementSpec(nil, []int{5, 0}, datetimeFromTimestamp, nil),
		"replyContent":  NewElementSpec(nil, []int{7, 1}, nil, nil),
		"repliedAt":     NewElementSpec(nil, []int{7, 2, 0}, datetimeFromTimestamp, nil),
		"appVersion":    NewElementSpec(nil, []int{10}, nil, nil),
	},
}

func parseDOM(ctx context.Context, dom, appID, url string) (map[string]interface{}, error) {

	matches := scriptRegex.FindAllStringSubmatch(dom, -1)

	dataset := make(map[int]interface{})

	for _, match := range matches {

		keyMatch := keyRegex.FindStringSubmatch(match[1])
		valueMatch := valueRegex.FindStringSubmatch(match[1])

		if len(keyMatch) > 1 && len(valueMatch) > 1 {
			key, _ := strconv.Atoi(strings.Split(keyMatch[1], ":")[1])

			var value interface{}
			err := json.Unmarshal([]byte(valueMatch[1]), &value)

			if err != nil {
				fmt.Printf("Error unmarshalling JSON: %v", err)

				return nil, err
			}

			dataset[key] = value
		}
	}

	result := make(map[string]interface{})

	for k, spec := range ElementSpecs["Detail"].(map[string]*ElementSpec) {
		content := spec.ExtractContent(dataset)

		if content == nil {
			result[k] = spec.FallbackValue
		} else {
			result[k] = content
		}

	}

	result["appId"] = appID
	result["url"] = url

	return result, nil

}

func appDetails(ctx context.Context, appID, lang, country string) (map[string]interface{}, error) {
	url := fmt.Sprintf(appDetailsURLFormat, appID, lang, country)

	body, err := get(ctx, url)
	if err != nil {
		fmt.Printf("Error retrieving app details: %v", err)
		return nil, err
	}

	return parseDOM(ctx, string(body), appID, url)
}

func appReviews(ctx context.Context, appID, lang, country string) (map[string]interface{}, error) {

	_reviews, _, err := reviews(ctx, appID, lang, country, Newest, 100, nil, nil, nil)
	if err != nil {
		// Handle error
	}

	for _, review := range _reviews {
		fmt.Println(review["content"])
	}

	// Get the next page of reviews
	/*if token != nil {
		moreReviews, nextToken, err := reviews(ctx, appID, "en", "us", Newest, 100, nil, nil, token)
	}*/
	return nil, nil
}

func main() {
	ctx := context.Background() // Or use your appropriate context
	//Example usage
	appId := "com.sega.sonicrumble"

	// appDetails, err := appDetails(ctx, appId, "en", "us")
	// if err != nil {
	// 	fmt.Printf("Error retrieving app details: %v", err)

	// 	return
	// }

	// fmt.Printf("Title: %s\nSummary: %s\nInstalls: %d\nDescription: %s", appDetails["title"], appDetails["summary"], int(appDetails["realInstalls"].(float64)), appDetails["description"])

	appReviews(ctx, appId, "en", "us")
}
