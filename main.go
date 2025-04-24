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
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

// Implementation

type Review struct {
	ReviewID         string `bigquery:"review_id"`
	AuthorName       string `bigquery:"author_name"`
	AppName          string `bigquery:"app_name"`
	Version          string `bigquery:"version"`
	Comments         string `bigquery:"comments"`
	StarRating       int64  `bigquery:"star_rating"`
	LastModified     string `bigquery:"last_modified"` // Format: RFC3339
	ReviewerLanguage string `bigquery:"reviewer_language"`
}

var (
	projectID     string
	bqClient      *bigquery.Client
	httpClient    *http.Client
	datasetID     string = "play_store_reviews_demo"
	tableID       string = "raw_reviews"
	ctx           context.Context
	reviewsApiUri = "androidpublisher.googleapis.com"
)

func init() {
	var err error

	ctx = context.Background()

	projectID = os.Getenv("PROJECT_ID")
	if projectID == "" {
		log.Fatal("PROJECT_ID environment variable must be set.")
	}

	bqClient, err = bigquery.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("bigquery.NewClient: %v", err)
	}
	defer bqClient.Close()

	httpClient, err = google.DefaultClient(ctx, "https://www.googleapis.com/auth/androidpublisher") // Use the correct scope
	if err != nil {
		log.Fatalf("Unable to create client: %v", err)
	}
}

func fetchReviews(packageName string, reviewsToFetch int) []*Review {
	baseURL := fmt.Sprintf("https://%s/androidpublisher/v3/applications/%s/reviews", reviewsApiUri, packageName)
	pageToken := ""
	var allReviews []*Review // Now a slice of our custom Review struct
	fetchedReviews := 0

	for {
		url := baseURL
		if pageToken != "" {
			url += "?token=" + pageToken + "&maxResults=" + strconv.Itoa(reviewsToFetch)
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Fatalf("Error creating request: %v", err)
		}

		token, err := httpClient.Transport.(*oauth2.Transport).Source.Token()
		if err != nil {
			log.Fatalf("Error getting token: %v", err)
		}

		req.Header.Set("Authorization", "Bearer "+token.AccessToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("Error making request: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Error reading response body: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusNotFound {
				log.Printf("No reviews found for package: %s", packageName)
				return nil
			}
			log.Fatalf("Error: %s, Status Code: %d", string(body), resp.StatusCode)
		}

		var reviewsResponse struct {
			Reviews []struct {
				ReviewId   string     `json:"reviewId"`
				AuthorName string     `json:"authorName"`
				Comments   []struct { // Comments is now a slice of structs
					UserComment struct {
						Text           string `json:"text"` // Extract the actual comment text
						StarRating     int64  `json:"starRating"`
						AppVersionName string `json:"appVersionName"`
						LastModified   struct {
							Seconds int64 `json:"seconds"`
							Nanos   int64 `json:"nanos"`
						} `json:"lastModified"`

						// Add other fields from userComment as needed
					} `json:"userComment"`
				} `json:"comments"`
				ReviewerLanguage string `json:"reviewerLanguage"`
			} `json:"reviews"`
			TokenPagination struct {
				NextPageToken string `json:"nextPageToken"`
			} `json:"tokenPagination"`
		}

		if err := json.Unmarshal(body, &reviewsResponse); err != nil {
			log.Fatalf("Error unmarshalling response: %v, Body: %s", err, string(body))
		}

		for _, r := range reviewsResponse.Reviews {

			t := time.Unix(int64(r.Comments[0].UserComment.LastModified.Seconds), 0) // Convert to time.Time
			formattedTimeWithFractional := t.Format("2006-01-02 15:04:05.000000")    // Format with fractional seconds (microseconds)

			allReviews = append(allReviews, &Review{
				ReviewID:         r.ReviewId,
				AuthorName:       r.AuthorName,
				AppName:          packageName,
				Comments:         r.Comments[0].UserComment.Text,       // Get the comment text
				StarRating:       r.Comments[0].UserComment.StarRating, // moved here from above level
				Version:          r.Comments[0].UserComment.AppVersionName,
				LastModified:     formattedTimeWithFractional,
				ReviewerLanguage: r.ReviewerLanguage,
			})
			fetchedReviews++

			if fetchedReviews >= reviewsToFetch {
				break
			}

		}

		if reviewsResponse.TokenPagination.NextPageToken == "" || fetchedReviews >= reviewsToFetch {
			break
		}

		fmt.Printf("Fetched %d vs %d\n", fetchedReviews, reviewsToFetch)

		pageToken = reviewsResponse.TokenPagination.NextPageToken
	}

	return allReviews
}

func pushToBigQuery(allReviews []*Review) {
	// check if allreviews is not nil nor empty
	if allReviews == nil {
		fmt.Println("No reviews fetched.")
		return
	}

	var bqReviews []*Review
	for _, review := range allReviews {

		bqReviews = append(bqReviews, &Review{
			ReviewID:         review.ReviewID,
			AuthorName:       review.AuthorName,
			AppName:          review.AppName,
			Version:          review.Version,
			Comments:         review.Comments,
			StarRating:       review.StarRating,
			LastModified:     review.LastModified,
			ReviewerLanguage: review.ReviewerLanguage,
		})
	}

	u := bqClient.Dataset(datasetID).Table(tableID).Inserter()
	if err := u.Put(ctx, bqReviews); err != nil {
		log.Fatalf("Failed to insert reviews into BigQuery: %v\n", err)
	}
}

func preProcessReviewsInBigQuery(packageName string) {
	defer bqClient.Close()

	// start timer
	start := time.Now()

	q := bqClient.Query(fmt.Sprintf("CALL `%s.pre_process_reviews_in_bq`('%s')", datasetID, packageName))
	q.Location = "US"

	job, err := q.Run(ctx)
	if err != nil {
		log.Fatalf("Error running stored procedure: %v", err)
	}

	status, err := job.Wait(ctx)
	if err != nil {
		log.Fatalf("Error waiting for job completion: %v", err)
	}

	if err := status.Err(); err != nil {
		log.Fatalf("Stored procedure execution failed: %v", err)
	}

	fmt.Printf("Review pre-processing with Gemini completed in %.2f seconds.\n", time.Since(start).Seconds())
}

func getVersions(packageName string) []string {
	defer bqClient.Close()

	query := bqClient.Query(fmt.Sprintf(`
		SELECT DISTINCT version
		FROM %s.reviews_to_process
		WHERE app_name = '%s'
	`, datasetID, packageName))

	it, err := query.Read(ctx)
	if err != nil {
		log.Fatalf("Failed to execute query: %v", err)
	}

	var versions []string
	for {
		var row map[string]bigquery.Value
		err = it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("Failed to read row: %v", err)
		}
		versions = append(versions, row["version"].(string))
	}

	return versions
}

func getVersionAnalysis(packageName string, version string) (string, error) {
	defer bqClient.Close()

	query := bqClient.Query(fmt.Sprintf(`
		SELECT gemini_response
		FROM %s.reviews_to_process
		WHERE version = '%s' AND app_name = '%s'
		ORDER BY created_at DESC
		LIMIT 1
	`, datasetID, version, packageName))

	it, err := query.Read(ctx)
	if err != nil {
		log.Fatalf("Failed to execute query: %v", err)
	}

	type GeminiResponse struct {
		Summary string `json:"summary"`
		Details []struct {
			CommentID string `json:"comment_id"`
			Tags      string `json:"tags"`
		} `json:"details"`
	}

	var row []bigquery.Value
	err = it.Next(&row)
	if err != nil {
		if err == io.EOF { // Handle case where no results are returned
			return "", nil
		}
		return "", fmt.Errorf("failed to retrieve next row: %w", err) // Wrap error
	}

	geminiJSON := row[0].(string)
	// remove first 7 and last 3 characters from this string
	geminiJSON = strings.Replace(geminiJSON, "```json", "", -1)
	geminiJSON = strings.Replace(geminiJSON, "```", "", -1)

	var geminiResponse GeminiResponse
	err = json.Unmarshal([]byte(geminiJSON), &geminiResponse)

	if err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w", err) // Wrap error
	}

	// fmt.Printf("Summary: %s\n", geminiResponse.Summary)
	// fmt.Println("Details:")
	// for _, tag := range geminiResponse.Details {
	// 	if tag.Tags == "" {
	// 		continue
	// 	}
	// 	fmt.Printf("  Comment ID: %s, Tags: %s\n", tag.CommentID, tag.Tags)
	// }

	// Convert to JSON string for returning in the response
	jsonData, err := json.Marshal(geminiResponse)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err) // Wrap error
	}

	return string(jsonData), nil // Return JSON string and nil error
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html")) // Create index.html
	tmpl.Execute(w, nil)
}

func fetchHandler(w http.ResponseWriter, r *http.Request) {
	packageName := r.URL.Query().Get("package_name")
	if packageName == "" {
		http.Error(w, "Package name is required", http.StatusBadRequest)
		return
	}

	reviews := fetchReviews(packageName, 200)
	pushToBigQuery(reviews)
	preProcessReviewsInBigQuery(packageName)

	fmt.Fprintln(w, "Reviews fetched, pushed to BigQuery, and pre-processed successfully!")
}

func analyzeHandler(w http.ResponseWriter, r *http.Request) {
	packageName := r.URL.Query().Get("package_name")
	if packageName == "" {
		http.Error(w, "Package name is required", http.StatusBadRequest)
		return
	}

	versions := getVersions(packageName)
	if len(versions) == 0 {
		http.Error(w, "No versions found for this package.", http.StatusNotFound)
		return
	}

	// Convert versions to JSON
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(versions) // Directly encode the versions slice
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func versionAnalysisHandler(w http.ResponseWriter, r *http.Request) {
	packageName := r.URL.Query().Get("package_name")
	version := r.URL.Query().Get("version")

	if packageName == "" || version == "" {
		http.Error(w, "Package name and Version are required", http.StatusBadRequest)
		return
	}

	jsonData, err := getVersionAnalysis(packageName, version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError) // Handle error properly
		return
	}

	if jsonData == "" { // Handle case where no analysis is found
		http.Error(w, "No analysis found for this version.", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, jsonData) // Write JSON to response
}

func commentHandler(w http.ResponseWriter, r *http.Request) {
	packageName := r.URL.Query().Get("package_name")
	commentID := r.URL.Query().Get("comment_id")

	if packageName == "" || commentID == "" {
		http.Error(w, "Package name and comment ID are required", http.StatusBadRequest)
		return
	}

	// Query BigQuery for the comment details
	query := bqClient.Query(fmt.Sprintf(`
		SELECT *
		FROM %s.raw_reviews
		WHERE app_name = '%s' AND review_id = '%s'
	`, datasetID, packageName, commentID))

	fmt.Print(fmt.Sprintf(`
	SELECT *
	FROM %s.raw_reviews
	WHERE app_name = '%s' AND review_id = '%s'
`, datasetID, packageName, commentID))

	it, err := query.Read(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to execute query: %v", err), http.StatusInternalServerError)
		return
	}

	type CommentDetails struct {
		ReviewID         string    `bigquery:"review_id"`
		AuthorName       string    `bigquery:"author_name"`
		AppName          string    `bigquery:"app_name"`
		Comments         string    `bigquery:"comments"`
		StarRating       int64     `bigquery:"star_rating"`
		LastModified     time.Time `bigquery:"last_modified"`
		ReviewerLanguage string    `bigquery:"reviewer_language"`
		Version          string    `bigquery:"version"` // Add Version field
	}

	var commentDetails CommentDetails
	err = it.Next(&commentDetails)
	if err != nil {
		if err == iterator.Done {
			http.Error(w, "Comment not found", http.StatusNotFound)
			return
		}

		http.Error(w, fmt.Sprintf("Failed to fetch comment: %v", err), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commentDetails)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mockURI := os.Getenv("MOCK_URI")
	if mockURI != "" {
		reviewsApiUri = mockURI
	}

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/fetch", fetchHandler)
	http.HandleFunc("/analyze", analyzeHandler)
	http.HandleFunc("/versionAnalysis", versionAnalysisHandler)
	http.HandleFunc("/comment", commentHandler)

	log.Printf("Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
