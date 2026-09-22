package models

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
	// "recipes", "system", or "translations".
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Destructive bool              `json:"destructive"`
	Params      []AdminRouteParam `json:"params"`
}
