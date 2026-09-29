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
		ID: "admin.recipes.favorites", Method: "GET", Path: "/admin/recipes/:id/favorites", Category: "recipes",
		Label: "List recipe favorites", Description: "See who favorited a recipe, paginated.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Picker: "recipe", Required: true, Label: "Recipe"},
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
	{
		ID: "admin.translations.listSuggestions", Method: "GET", Path: "/admin/translation-suggestions", Category: "translations",
		Label: "List translation suggestions", Description: "Browse user-submitted translation fixes awaiting review.",
		Params: []models.AdminRouteParam{
			{Name: "status", In: "query", Type: "string", Label: "Status"},
			{Name: "limit", In: "query", Type: "int", Label: "Limit"},
			{Name: "offset", In: "query", Type: "int", Label: "Offset"},
		},
	},
	{
		ID: "admin.translations.approve", Method: "POST", Path: "/admin/translation-suggestions/:id/approve", Category: "translations",
		Label: "Approve translation suggestion", Description: "Apply a submitted fix to the live translation and pin it as a durable override.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.translations.reject", Method: "POST", Path: "/admin/translation-suggestions/:id/reject", Category: "translations",
		Label: "Reject translation suggestion", Description: "Dismiss a submitted fix with no effect.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.translations.listOverrides", Method: "GET", Path: "/admin/translation-overrides", Category: "translations",
		Label: "List durable translation overrides", Description: "See which fields of a recipe's translation are pinned by an approved fix.",
		Params: []models.AdminRouteParam{
			{Name: "recipe_id", In: "query", Type: "string", Picker: "recipe", Required: true, Label: "Recipe"},
			{Name: "locale", In: "query", Type: "string", Label: "Locale"},
		},
	},
	{
		ID: "admin.translations.clearOverride", Method: "DELETE", Path: "/admin/translation-overrides/:id", Category: "translations",
		Label: "Clear translation override", Description: "Revert one pinned field back to machine translation immediately.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Override ID"},
		},
	},
	{
		ID: "admin.toolbox.listSuggestions", Method: "GET", Path: "/admin/toolbox-suggestions", Category: "toolbox",
		Label: "List toolbox suggestions", Description: "Browse user-submitted Toolbox ingredient/unit/substitution additions and corrections awaiting review.",
		Params: []models.AdminRouteParam{
			{Name: "status", In: "query", Type: "string", Label: "Status"},
			{Name: "kind", In: "query", Type: "string", Label: "Kind (ingredient / unit / substitution)"},
			{Name: "limit", In: "query", Type: "int", Label: "Limit"},
			{Name: "offset", In: "query", Type: "int", Label: "Offset"},
		},
	},
	{
		ID: "admin.toolbox.approve", Method: "POST", Path: "/admin/toolbox-suggestions/:id/approve", Category: "toolbox",
		Label: "Approve toolbox suggestion", Description: "Apply a submitted addition or correction to the live Toolbox reference list.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.toolbox.reject", Method: "POST", Path: "/admin/toolbox-suggestions/:id/reject", Category: "toolbox",
		Label: "Reject toolbox suggestion", Description: "Dismiss a submitted Toolbox addition/correction with no effect.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.nutrition.listSuggestions", Method: "GET", Path: "/admin/nutrition-suggestions", Category: "nutrition",
		Label: "List nutrition suggestions", Description: "Browse user-submitted ingredient nutrition link additions and corrections awaiting review.",
		Params: []models.AdminRouteParam{
			{Name: "status", In: "query", Type: "string", Label: "Status"},
			{Name: "limit", In: "query", Type: "int", Label: "Limit"},
			{Name: "offset", In: "query", Type: "int", Label: "Offset"},
		},
	},
	{
		ID: "admin.nutrition.approve", Method: "POST", Path: "/admin/nutrition-suggestions/:id/approve", Category: "nutrition",
		Label: "Approve nutrition suggestion", Description: "Apply a submitted link addition/correction to the live ingredient nutrition link table.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.nutrition.reject", Method: "POST", Path: "/admin/nutrition-suggestions/:id/reject", Category: "nutrition",
		Label: "Reject nutrition suggestion", Description: "Dismiss a submitted nutrition link addition/correction with no effect.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Suggestion ID"},
		},
	},
	{
		ID: "admin.nutrition.createLink", Method: "POST", Path: "/admin/nutrition-links", Category: "nutrition",
		Label: "Create nutrition link", Description: "Directly link an ingredient name to a nutrition entry - applies immediately, no review.",
		Params: []models.AdminRouteParam{
			{Name: "ingredient_name", In: "body", Type: "string", Required: true, Label: "Ingredient name"},
			{Name: "nutrition_id", In: "body", Type: "string", Required: true, Label: "Nutrition entry ID"},
			{Name: "g_per_100ml", In: "body", Type: "string", Label: "Density (g/100ml)"},
			{Name: "grams_per_unit", In: "body", Type: "string", Label: "Grams per unit"},
		},
	},
	{
		ID: "admin.nutrition.updateLink", Method: "PUT", Path: "/admin/nutrition-links/:id", Category: "nutrition",
		Label: "Update nutrition link", Description: "Directly overwrite an existing ingredient nutrition link - applies immediately, no review.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Link ID"},
			{Name: "ingredient_name", In: "body", Type: "string", Required: true, Label: "Ingredient name"},
			{Name: "nutrition_id", In: "body", Type: "string", Required: true, Label: "Nutrition entry ID"},
			{Name: "g_per_100ml", In: "body", Type: "string", Label: "Density (g/100ml)"},
			{Name: "grams_per_unit", In: "body", Type: "string", Label: "Grams per unit"},
		},
	},
	{
		ID: "admin.nutrition.deleteLink", Method: "DELETE", Path: "/admin/nutrition-links/:id", Category: "nutrition",
		Label: "Delete nutrition link", Description: "Revert an ingredient back to fully unlinked.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Link ID"},
		},
	},
	{
		ID: "admin.nutrition.createUnitAlias", Method: "POST", Path: "/admin/unit-aliases", Category: "nutrition",
		Label: "Create unit alias", Description: "Directly map a free-text unit string to a Toolbox unit - applies immediately, no review.",
		Params: []models.AdminRouteParam{
			{Name: "alias", In: "body", Type: "string", Required: true, Label: "Alias text"},
			{Name: "unit_id", In: "body", Type: "string", Required: true, Label: "Toolbox unit ID"},
		},
	},
	{
		ID: "admin.nutrition.updateUnitAlias", Method: "PUT", Path: "/admin/unit-aliases/:id", Category: "nutrition",
		Label: "Update unit alias", Description: "Directly overwrite an existing unit alias - applies immediately, no review.",
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Alias ID"},
			{Name: "alias", In: "body", Type: "string", Required: true, Label: "Alias text"},
			{Name: "unit_id", In: "body", Type: "string", Required: true, Label: "Toolbox unit ID"},
		},
	},
	{
		ID: "admin.nutrition.deleteUnitAlias", Method: "DELETE", Path: "/admin/unit-aliases/:id", Category: "nutrition",
		Label: "Delete unit alias", Description: "Remove a unit alias outright.",
		Destructive: true,
		Params: []models.AdminRouteParam{
			{Name: "id", In: "path", Type: "string", Required: true, Label: "Alias ID"},
		},
	},
}
