// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"strings"

	"github.com/gorilla/mux"
	"golang.org/x/net/context"
)

const (
	playStoreBaseURL = "https://play.google.com"
)

var (
	reviewsRegex     = regexp.MustCompile(`\)]}'\n\n([\s\S]+)`)
	reviewsURLFormat = playStoreBaseURL + "/_/PlayStoreUi/data/batchexecute?hl=%s&gl=%s"
)

var reviewSpecs = map[string]*ElementSpec{
	"reviewId":      {nil, []int{0}, nil, nil},
	"userName":      {nil, []int{1, 0}, nil, nil},
	"userImage":     {nil, []int{1, 1, 3, 2}, nil, nil},
	"content":       {nil, []int{4}, nil, nil},
	"score":         {nil, []int{2}, nil, nil},
	"thumbsUpCount": {nil, []int{6}, nil, nil},
	"at":            {nil, []int{5, 0}, datetimeFromTimestamp, nil},
	"replyContent":  {nil, []int{7, 1}, nil, nil},
	"repliedAt":     {nil, []int{7, 2, 0}, datetimeFromTimestamp, nil},
	"appVersion":    {nil, []int{10}, nil, nil},
}

func FetchReviews(ctx context.Context, appID, lang, country string, sort Sort, count int, filterScoreWith, filterDeviceWith *int, continuationToken *ContinuationToken) ([]map[string]interface{}, *ContinuationToken, error) {

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
	maxCountEachFetch := 1000
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
			for k, spec := range reviewSpecs {
				content := spec.ExtractContent(reviewMap)

				if content != nil {
					reviewResult[k] = content
				}

				if k == "appVersion" && content == nil {
					reviewResult[k] = ""
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
				MaxCountEachFetch: 1000,
			}
		} else {
			continuationToken.Token = token

		}

	}

	return result, continuationToken, nil
}

func fetchReviewItems(ctx context.Context, fetchUrl, appID string, sort Sort, count int, filterScoreWith, filterDeviceWith *int, paginationToken string) ([]interface{}, string, error) {

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

	data, _ := url.ParseQuery(payloadFormat)

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

	if len(response) == 0 || len(response[0].([]interface{})) == 0 || response[0].([]interface{})[2] == nil {
		return nil, "", nil
	}

	var token string
	if len(response[0].([]interface{})) >= 4 {
		t := response[0].([]interface{})[2].(string)

		var ut []interface{}
		err = json.Unmarshal([]byte(t), &ut)
		if err != nil {
			return nil, "", err
		}

		if len(ut) >= 3 {
			token = ut[len(ut)-2].([]interface{})[1].(string)
		}

	}

	mr := response[0].([]interface{})[2].(string)

	var _reviewItems []interface{}
	err = json.Unmarshal([]byte(mr), &_reviewItems)
	if err != nil {
		return nil, "", err
	}

	reviewItems := _reviewItems[0].([]interface{})

	return reviewItems, token, nil
}

// TransformReviews transforms reviews into the desired format that mocks real Play! store
func transformReviews(reviewData []map[string]interface{}, pageInfo map[string]int, nextPageToken, previousPageToken string) (string, error) {
	transformed := ReviewsResponse{
		Reviews: []TransformedReview{},
		TokenPagination: struct {
			NextPageToken     string `json:"nextPageToken"`
			PreviousPageToken string `json:"previousPageToken"`
		}{
			NextPageToken:     nextPageToken,
			PreviousPageToken: previousPageToken,
		},
		PageInfo: struct {
			TotalResults  int `json:"totalResults"`
			ResultPerPage int `json:"resultPerPage"`
			StartIndex    int `json:"startIndex"`
		}{
			TotalResults:  pageInfo["totalResults"],
			ResultPerPage: pageInfo["resultPerPage"],
			StartIndex:    pageInfo["startIndex"],
		},
	}

	for _, review := range reviewData {
		unixTimestamp := time.Now().Unix() // Default if we fail parsing
		timestampStr := review["at"].(string)
		t, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			fmt.Printf("Error parsing timestamp: %s. Defaulting to Now()\n", err)
		} else {
			// Get the Unix timestamp (seconds since the Unix epoch).
			unixTimestamp = t.Unix()
		}

		transformed.Reviews = append(transformed.Reviews, TransformedReview{
			ReviewID:   review["reviewId"].(string),
			AuthorName: review["userName"].(string),
			Comments: []Comment{
				{
					UserComment: UserComment{
						Text: review["content"].(string),
						LastModified: Time{
							Seconds: unixTimestamp,
							Nanos:   0,
						},
						StarRating:       int(review["score"].(float64)),
						ReviewerLanguage: "en", // Replace as needed
						Device:           "",   // Replace as needed
						AndroidOsVersion: 0,    // Replace as needed
						AppVersionCode:   0,    // Replace as needed
						AppVersionName:   review["appVersion"].(string),
						ThumbsUpCount:    int(review["thumbsUpCount"].(float64)),
						ThumbsDownCount:  0, // Replace as needed
						DeviceMetadata:   MockPhones[rand.Intn(len(MockPhones))],
						OriginalText:     review["content"].(string),
					},
				},
			},
		})
	}

	jsonOutput, err := json.MarshalIndent(transformed, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonOutput), nil
}

// handles the /reviews endpoint
func reviewsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	appID := vars["app_id"]

	pageToken := r.URL.Query().Get("token")
	countStr := r.URL.Query().Get("maxResults")
	lang := r.URL.Query().Get("lang")
	country := r.URL.Query().Get("country")
	filterScoreWithStr := r.URL.Query().Get("filter_score_with")

	count := 100 // Default count
	if countStr != "" {
		var err error
		count, err = strconv.Atoi(countStr)
		if err != nil {
			http.Error(w, "Invalid maxResults parameter", http.StatusBadRequest)
			return
		}
	}

	var filterScoreWith *int
	if filterScoreWithStr != "" {
		score, err := strconv.Atoi(filterScoreWithStr)
		if err != nil {
			http.Error(w, "Invalid filter_score_with parameter", http.StatusBadRequest)
			return
		}
		filterScoreWith = &score
	}

	if lang == "" {
		lang = "en"
	}
	if country == "" {
		country = "us"
	}

	var ct *ContinuationToken
	if pageToken != "" {
		ct = &ContinuationToken{Token: pageToken, Lang: lang, Country: country, Sort: 2, Count: count, FilterScoreWith: filterScoreWith, FilterDeviceWith: nil, MaxCountEachFetch: 1000}
	}

	result, continuationToken, err := FetchReviews(context.Background(), appID, lang, country, Newest, count, filterScoreWith, nil, ct)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching reviews: %v", err), http.StatusInternalServerError)
		return
	}

	startIndex := 0
	nextPageToken := ""
	if continuationToken != nil {
		startIndex = -1
		nextPageToken = continuationToken.Token
	}

	// -1 is a mock value because we don't really know how many total results there is. Nor we know the start index
	jsonOutput, err := transformReviews(result, map[string]int{"totalResults": count, "resultPerPage": len(result), "startIndex": startIndex}, nextPageToken, "")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error transforming reviews: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, jsonOutput)
}

func post(ctx context.Context, url string, data url.Values, headers map[string]string) ([]byte, error) {
	client := &http.Client{}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(data.Encode()))
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

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/androidpublisher/v3/applications/{app_id}/reviews", reviewsHandler).Methods("GET")

	fmt.Println("Play! Mock Server listening on port 8080")
	http.ListenAndServe(":8080", r)
}
