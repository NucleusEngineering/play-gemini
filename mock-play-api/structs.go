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

import "time"

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

// ContinuationToken represents the continuation token structure
type ContinuationToken struct {
	Token             string `json:"token"`
	Lang              string `json:"lang"`
	Country           string `json:"country"`
	Sort              Sort   `json:"sort"`
	Count             int    `json:"count"`
	FilterScoreWith   *int   `json:"filterScoreWith"` // Use pointers for optional fields
	FilterDeviceWith  *int   `json:"filterDeviceWith"`
	MaxCountEachFetch int    `json:"maxCountEachFetch"`
}

// Review represents a simplified review structure
type Review struct {
	ReviewID             string    `json:"reviewId"`
	UserName             string    `json:"userName"`
	Content              string    `json:"content"`
	Score                int       `json:"score"`
	ThumbsUpCount        int       `json:"thumbsUpCount"`
	ReviewCreatedVersion string    `json:"reviewCreatedVersion"`
	At                   time.Time `json:"at"`
}

// ReviewsResponse represents the response structure for reviews
type ReviewsResponse struct {
	Reviews         []TransformedReview `json:"reviews"`
	TokenPagination struct {
		NextPageToken     string `json:"nextPageToken"`
		PreviousPageToken string `json:"previousPageToken"`
	} `json:"tokenPagination"`
	PageInfo struct {
		TotalResults  int `json:"totalResults"`
		ResultPerPage int `json:"resultPerPage"`
		StartIndex    int `json:"startIndex"`
	} `json:"pageInfo"`
}

// TransformedReview represents the transformed review structure
type TransformedReview struct {
	ReviewID   string    `json:"reviewId"`
	AuthorName string    `json:"authorName"`
	Comments   []Comment `json:"comments"`
}

// Comment represents a comment structure
type Comment struct {
	UserComment UserComment `json:"userComment"`
}

// UserComment represents a user comment structure
type UserComment struct {
	Text             string      `json:"text"`
	LastModified     Time        `json:"lastModified"`
	StarRating       int         `json:"starRating"`
	ReviewerLanguage string      `json:"reviewerLanguage"`
	Device           string      `json:"device"`
	AndroidOsVersion int         `json:"androidOsVersion"`
	AppVersionCode   int         `json:"appVersionCode"`
	AppVersionName   string      `json:"appVersionName"`
	ThumbsUpCount    int         `json:"thumbsUpCount"`
	ThumbsDownCount  int         `json:"thumbsDownCount"`
	DeviceMetadata   interface{} `json:"deviceMetadata"`
	OriginalText     string      `json:"originalText"`
}

// Time represents a time structure
type Time struct {
	Seconds int64 `json:"seconds"`
	Nanos   int   `json:"nanos"`
}

type ElementSpec struct {
	DsNum         *int
	DataMap       []int
	PostProcessor func(interface{}) interface{}
	FallbackValue interface{}
}
