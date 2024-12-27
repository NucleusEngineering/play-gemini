package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	projectID  string
	bqClient   *bigquery.Client
	httpClient *http.Client
	datasetID  string = "play_store_reviews_demo"
	tableID    string = "raw_reviews"
	ctx        context.Context
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
	baseURL := fmt.Sprintf("http://localhost:8080/androidpublisher/v3/applications/%s/reviews", packageName) // androidpublisher.googleapis.com
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

	q := bqClient.Query(fmt.Sprintf("CALL `play_store_reviews_demo.pre_process_reviews_in_bq`('%s')", packageName)) // Add dataset name
	q.Location = "US"                                                                                               // Or your preferred location

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
		FROM play_store_reviews_demo.reviews_to_process
		WHERE app_name = '%s'
	`, packageName))

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

func getVersionAnalysis(packageName string, version string) {
	defer bqClient.Close()

	query := bqClient.Query(fmt.Sprintf(`
		SELECT gemini_response
		FROM play_store_reviews_demo.reviews_to_process
		WHERE version = '%s' AND app_name = '%s'
		ORDER BY created_at DESC
		LIMIT 1
	`, version, packageName))

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
			fmt.Printf("No analysis found for version: %s\n", version)
			return
		}
		log.Fatalf("Failed to retrieve next row: %v", err)
	}

	geminiJSON := row[0].(string)
	// remove first 7 and last 3 characters from this string
	geminiJSON = strings.Replace(geminiJSON, "```json", "", -1)
	geminiJSON = strings.Replace(geminiJSON, "```", "", -1)

	var geminiResponse GeminiResponse
	err = json.Unmarshal([]byte(geminiJSON), &geminiResponse)

	if err != nil {
		fmt.Printf("%s", geminiJSON)
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	fmt.Printf("Summary: %s\n", geminiResponse.Summary)
	fmt.Println("Details:")
	for _, tag := range geminiResponse.Details {
		if tag.Tags == "" {
			continue
		}
		fmt.Printf("  Comment ID: %s, Tags: %s\n", tag.CommentID, tag.Tags)
	}
}

func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter the package name (eg. com.supercell.squad): ")
	packageName, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Error reading input: %v", err)
	}
	packageName = strings.TrimSpace(packageName)

	fmt.Printf("Fetching reviews for \"%s\"\n", packageName)
	reviews := fetchReviews(packageName, 50)

	fmt.Printf("Pushing %d reviews to Bigquery\n", len(reviews))
	pushToBigQuery(reviews)

	fmt.Printf("Processing reviews with gemini for \"%s\"\n", packageName)
	preProcessReviewsInBigQuery(packageName)

	versions := getVersions(packageName)

	if len(versions) == 0 {
		fmt.Println("No versions found for this package.")
		return
	}

	fmt.Println("Available versions:")
	for i, version := range versions {
		fmt.Printf("%d. %s\n", i+1, version)
	}

	reader = bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter the number of the version to analyze (or 0 to exit): ")
		choiceStr, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Error reading input: %v", err)
		}
		choiceStr = strings.TrimSpace(choiceStr)
		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 0 || choice > len(versions) {
			fmt.Println("Invalid choice. Please enter a valid number.")
			continue
		}

		if choice == 0 {
			break // Exit the loop if the user enters 0
		}

		selectedVersion := versions[choice-1]
		fmt.Printf("Version \"%s\" analysis:\n", selectedVersion)
		getVersionAnalysis(packageName, selectedVersion)
		fmt.Println("---")
		break // Exit the loop after displaying the chosen version
	}
}
