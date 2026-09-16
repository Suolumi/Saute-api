package handlers

import "recipes/internal/models"

// adminRouteManifest is the single source of truth behind GET /admin/routes,
// which drives the back-office's generic "call any admin route" console.
// Every admin-gated route - including the five that predate the /admin group
// - gets one entry here, added in the same change as the route itself. There
// is no reflection-based discovery: Echo's route table has no notion of a
// param's picker type, category, or whether a method is destructive, so this
// has to be hand-maintained.
var adminRouteManifest = []models.AdminRouteDescriptor{
	{
		ID: "users.update", Method: "PUT", Path: "/users/:id", Category: "users",
		Label: "Update user", Description: "Change a user's username, email, or password.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
			{Name: "username", In: "body", Type: "string", Label: "Username"},
			{Name: "email", In: "body", Type: "string", Label: "Email"},
			{Name: "password", In: "body", Type: "string", Label: "New password"},
		},
	},
	{
		ID: "users.delete", Method: "DELETE", Path: "/users/:id", Category: "users",
		Label: "Delete user", Description: "Permanently delete a user and their profile picture.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
		},
	},
	{
		ID: "users.updatePicture", Method: "POST", Path: "/users/:id/picture", Category: "users",
		Label: "Replace user picture", Description: "Upload a new profile picture for a user.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
			{Name: "file", In: "body", Type: "file", Required: true, Label: "Picture"},
		},
	},
	{
		ID: "users.deletePicture", Method: "DELETE", Path: "/users/:id/picture", Category: "users",
		Label: "Remove user picture", Description: "Delete a user's profile picture.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
		},
	},
	{
		ID: "recipes.retranslate", Method: "POST", Path: "/recipes/:id/retranslate", Category: "recipes",
		Label: "Retranslate recipe", Description: "Force a fresh translation of one recipe into every configured locale.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "recipe", Required: true, Label: "Recipe"},
		},
	},
	{
		ID: "admin.users.list", Method: "GET", Path: "/admin/users", Category: "users",
		Label: "List users", Description: "Search/list users with pagination.",
		Params: []models.AdminRouteParam{
			{Name: "username", In: "query", Type: "string", Label: "Username contains"},
			{Name: "limit", In: "query", Type: "int", Label: "Limit"},
			{Name: "offset", In: "query", Type: "int", Label: "Offset"},
		},
	},
	{
		ID: "admin.users.setAdminStatus", Method: "PATCH", Path: "/admin/users/:id/admin-status", Category: "users",
		Label: "Promote / demote admin", Description: "Grant or remove a user's admin status.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
			{Name: "admin", In: "body", Type: "bool", Required: true, Label: "Admin"},
		},
	},
	{
		ID: "admin.users.sendPasswordReset", Method: "POST", Path: "/admin/users/:id/send-password-reset", Category: "users",
		Label: "Send password reset email", Description: "Trigger the password-reset email for a user.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
			{Name: "locale", In: "query", Type: "string", Label: "Email locale"},
		},
	},
	{
		ID: "admin.users.revokeMcpToken", Method: "DELETE", Path: "/admin/users/:id/mcp-token", Category: "users",
		Label: "Revoke MCP token", Description: "Invalidate every MCP token currently issued to a user.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "user", Required: true, Label: "User"},
		},
	},
	{
		ID: "recipes.linkVariation", Method: "PATCH", Path: "/recipes/:id/variation-of", Category: "recipes",
		Label: "Link recipe as variation", Description: "Turn an existing standalone recipe into a variation of another recipe.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "recipe", Required: true, Label: "Recipe to link"},
			{Name: "variation_of", In: "body", Type: "string", Picker: "recipe", Required: true, Label: "Target recipe"},
		},
	},
	{
		ID: "admin.recipes.detachVariation", Method: "DELETE", Path: "/admin/recipes/:id/variation-of", Category: "recipes",
		Label: "Detach recipe from family", Description: "Remove a variation from its family, turning it back into a standalone root.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "recipe", Required: true, Label: "Variation"},
		},
	},
	{
		ID: "admin.recipes.list", Method: "GET", Path: "/admin/recipes", Category: "recipes",
		Label: "List recipes (flat)", Description: "Browse every recipe and variation across all users, ungrouped.",
		Params: []models.AdminRouteParam{
			{Name: "title", In: "query", Type: "string", Label: "Title contains"},
			{Name: "author", In: "query", Type: "string", Label: "Author username"},
			{Name: "category", In: "query", Type: "string", Label: "Category"},
			{Name: "kind", In: "query", Type: "string", Label: "Kind"},
			{Name: "limit", In: "query", Type: "int", Label: "Limit"},
			{Name: "offset", In: "query", Type: "int", Label: "Offset"},
		},
	},
	{
		ID: "admin.system.stats", Method: "GET", Path: "/admin/system/stats", Category: "system",
		Label: "System stats", Description: "User/recipe counts.",
		Params: []models.AdminRouteParam{},
	},
	{
		ID: "admin.system.cleanupImages", Method: "POST", Path: "/admin/system/cleanup-images", Category: "system",
		Label: "Run image cleanup", Description: "Delete unreferenced picture files older than 24h, right now.",
		Params: []models.AdminRouteParam{},
	},
}
