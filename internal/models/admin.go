package models

import "time"

// AdminStats backs GET /admin/system/stats.
type AdminStats struct {
	TotalUsers        int64            `json:"total_users"`
	TotalAdmins       int64            `json:"total_admins"`
	TotalRecipes      int64            `json:"total_recipes"`
	RecipesByCategory map[string]int64 `json:"recipes_by_category"`
}

// CleanupImagesResult backs POST /admin/system/cleanup-images.
type CleanupImagesResult struct {
	Removed map[string]int `json:"removed"`
}

// AdminRouteParam describes one parameter of an AdminRouteDescriptor, telling
// a generic client where the value goes and how to collect it.
type AdminRouteParam struct {
	// Name is the parameter's identifier: a path placeholder (matching the
	// route's ":name" segment), a query key, or a body field name.
	Name string `json:"name"`
	// In is "path", "query", or "body".
	In string `json:"in"`
	// Type is "string", "int", "bool", or "file" (multipart upload).
	Type string `json:"type"`
	// Picker, when non-empty ("user" or "recipe"), tells the client to offer
	// a search-and-select UI for this parameter instead of a bare input.
	Picker   string `json:"picker,omitempty"`
	Required bool   `json:"required"`
	Label    string `json:"label"`
}

// AdminRouteDescriptor is one entry in the GET /admin/routes manifest that
// drives the back-office's generic "call any admin route" console. It is
// hand-maintained alongside each admin route's registration - the console has
// no other way to discover a route's shape.
type AdminRouteDescriptor struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Label  string `json:"label"`
	// Category groups actions in the console's left-hand list: "users",
	// "recipes", "translations", "toolbox", "nutrition", "tags", "feedback",
	// or "system".
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Destructive bool              `json:"destructive"`
	Params      []AdminRouteParam `json:"params"`
}

// AdminMailRequest backs POST /admin/mails/preview and /admin/mails/broadcast:
// the content of an announcement sent with the Sauté mail template.
type AdminMailRequest struct {
	Subject     string `json:"subject"`
	Heading     string `json:"heading"`
	Message     string `json:"message"`
	ButtonLabel string `json:"button_label,omitempty"`
	ButtonURL   string `json:"button_url,omitempty"`
	// TestOnly sends the mail to the calling admin only.
	TestOnly bool `json:"test_only,omitempty"`
}

// AdminMailPreview backs POST /admin/mails/preview.
type AdminMailPreview struct {
	HTML string `json:"html"`
}

// MailBroadcastFailure is one recipient a broadcast could not reach.
type MailBroadcastFailure struct {
	Email string `json:"email"`
	Error string `json:"error"`
}

// MailBroadcastStatus backs GET /admin/mails/broadcast: the progress of the
// running broadcast, or the outcome of the last one.
type MailBroadcastStatus struct {
	Running    bool                   `json:"running"`
	Subject    string                 `json:"subject,omitempty"`
	Total      int                    `json:"total"`
	Sent       int                    `json:"sent"`
	Failed     []MailBroadcastFailure `json:"failed"`
	StartedAt  *time.Time             `json:"started_at,omitempty"`
	FinishedAt *time.Time             `json:"finished_at,omitempty"`
}
