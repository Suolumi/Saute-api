package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	FeedbackBug  = "bug"
	FeedbackIdea = "idea"

	FeedbackOpen     = "open"
	FeedbackDone     = "done"
	FeedbackDeclined = "declined"
)

// Feedback is one bug report or idea posted on the public feedback board.
// Voters always holds the author (posting counts as their vote); VoteCount
// mirrors len(Voters) so the board can sort on it.
type Feedback struct {
	Id          *primitive.ObjectID  `bson:"_id,omitempty"`
	Type        string               `bson:"type"`
	Title       string               `bson:"title"`
	Description string               `bson:"description"`
	Photos      []string             `bson:"photos"`
	LinkPath    string               `bson:"link_path,omitempty"`
	Author      primitive.ObjectID   `bson:"author"`
	Status      string               `bson:"status"`
	Voters      []primitive.ObjectID `bson:"voters"`
	VoteCount   int                  `bson:"vote_count"`
	// SearchText is the lowercased, accent-stripped title + description the
	// board's search matches against.
	SearchText string `bson:"search_text"`
	// AdminSeen is false until an admin opens the item; unseen items feed the
	// admin pending badge.
	AdminSeen bool `bson:"admin_seen"`
	// AuthorUnread is set when an admin closes the item and cleared once the
	// author has seen the new status.
	AuthorUnread bool `bson:"author_unread"`
	// SourceLocale is the detected language of Title/Description, filled the
	// first time someone asks for a translation.
	SourceLocale string `bson:"source_locale,omitempty"`
	// Translations caches on-demand translations by locale. Editing the
	// feedback clears it.
	Translations map[string]FeedbackTranslation `bson:"translations,omitempty"`
	CreatedAt    time.Time                      `bson:"created_at"`
	UpdatedAt    time.Time                      `bson:"updated_at"`
	ClosedAt     *time.Time                     `bson:"closed_at,omitempty"`
}

// FeedbackTranslation is a Feedback's title and description in one locale.
type FeedbackTranslation struct {
	Title       string `bson:"title" json:"title"`
	Description string `bson:"description" json:"description"`
}

// TranslateFeedbackRequest is the body of POST /feedback/:id/translate.
type TranslateFeedbackRequest struct {
	Locale string `json:"locale"`
}

// TranslateFeedbackResponse is a feedback's text in Locale. SameLanguage
// means it was already written in Locale, so Title/Description are the
// originals.
type TranslateFeedbackResponse struct {
	FeedbackTranslation
	Locale       string `json:"locale"`
	SourceLocale string `json:"source_locale"`
	SameLanguage bool   `json:"same_language"`
}

// FeedbackContent is the author-editable part of a Feedback.
type FeedbackContent struct {
	Type        string
	Title       string
	Description string
	Photos      []string
	LinkPath    string
	SearchText  string
}

// FeedbackRequest is the body of POST /feedback and PATCH /feedback/:id (the
// "feedback" field of a multipart request). On PATCH, Photos lists the
// already-stored filenames to keep (omitted keeps them all); uploaded files
// are appended after them.
type FeedbackRequest struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	LinkPath    string   `json:"link_path"`
	Photos      []string `json:"photos"`
}

// SetFeedbackStatusRequest is the body of POST /admin/feedback/:id/status.
type SetFeedbackStatusRequest struct {
	Status string `json:"status"`
}

// ListFeedbackParams filters a feedback listing. Status "closed" matches both
// done and declined; "" matches every status. Without SortByVotes, closed
// statuses sort by close date, others by creation date.
type ListFeedbackParams struct {
	Status        string
	Type          string
	Query         string
	AuthorID      string
	AuthorUnread  bool
	AdminUnseen   bool
	SortByVotes   bool
	Limit, Offset int64
}

// FeedbackView is a Feedback as the API returns it: voter ids replaced by the
// viewer's own vote, the author resolved, and a recipe link's title.
type FeedbackView struct {
	Id          *primitive.ObjectID `json:"id"`
	Type        string              `json:"type"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Photos      []string            `json:"photos"`
	LinkPath    string              `json:"link_path,omitempty"`
	// LinkTitle is the linked recipe's title when LinkPath is a recipe page
	// that still exists.
	LinkTitle string    `json:"link_title,omitempty"`
	Author    *UserView `json:"author,omitempty"`
	// SourceLocale is set once someone has translated this feedback.
	SourceLocale string     `json:"source_locale,omitempty"`
	Status       string     `json:"status"`
	VoteCount    int        `json:"vote_count"`
	Voted        bool       `json:"voted"`
	Mine         bool       `json:"mine"`
	Unread       bool       `json:"unread,omitempty"`
	AdminSeen    *bool      `json:"admin_seen,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

type ListFeedbackResponse struct {
	Length int64          `json:"length"`
	Items  []FeedbackView `json:"items"`
}
