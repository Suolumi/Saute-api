# Sauté MCP server

The API exposes a stateless Streamable HTTP MCP server at `/mcp`. Point a remote MCP-capable agent at the public URL; for production that is:

```text
https://recipes-api.suolumi.fr/mcp
```

Authentication is a bearer token: the agent sends `Authorization: Bearer <token>` on every request. A token is a per-user MCP access token (see **Getting a token**). It is scoped to MCP only and cannot be used as a REST JWT; a REST JWT cannot be used here either. Tokens carry the two scopes `recipes:read` and `recipes:write`. Changing the account password, or calling `DELETE /api/v1/mcp/token`, invalidates every outstanding MCP token for that account; deleting the account invalidates it as well.

The server implements the current `2026-07-28` MCP protocol only. It uses the official Go MCP SDK and has no admin-specific behavior: every account, including an admin account, can access only recipes it authored through MCP.

## Getting a token

While logged in to the Sauté website, call the API once:

```text
POST /api/v1/mcp/token        (Authorization: Bearer <website access token>)
→ 200 { "token": "<mcp token>" }
```

Paste `token` into the local agent's MCP configuration as the bearer token for `<public-url>`. The token is a signed JWT — nothing is stored server-side — and is valid for `RECIPES_MCP_TOKENEXPIRATION` (default one year). Minting again returns a fresh token; both remain valid until expiry or revocation.

To cut off access:

```text
DELETE /api/v1/mcp/token      (Authorization: Bearer <website access token>)
```

This bumps the user's MCP auth version, so every token previously issued to them stops verifying immediately. They mint a new one to reconnect.

## Configuration

Set these environment variables in production:

```text
RECIPES_MCP_PUBLICURL=https://recipes-api.suolumi.fr/mcp
RECIPES_MCP_JWTSECRET=<at-least-32-random-bytes>
```

`RECIPES_MCP_PUBLICURL` must use HTTPS outside localhost and must have exactly the `/mcp` path; it also supplies the base for picture URLs returned by the tools. In local development it defaults to `http://localhost:<RECIPES_CFG_PORT>/mcp`. `RECIPES_MCP_JWTSECRET` signs the MCP tokens (HS256) and must be at least 32 bytes outside localhost; rotating it invalidates all outstanding MCP tokens.

Optional settings and defaults:

| Variable | Default |
| --- | --- |
| `RECIPES_MCP_TOKENEXPIRATION` | `8760h` |
| `RECIPES_MCP_MAXDECODEDPICTUREBYTES` | `67108864` |

Token validation is stateless apart from checking that the user still exists and that the token's embedded auth version still matches the account.

## Tools

- `list_my_recipes`: accepts optional `cursor`, `limit` (default 20, maximum 100), and one BCP 47 `locale`. It returns the total, items, and an opaque next cursor.
- `get_my_recipe`: accepts `recipe_id` and optional `locale`. Pictures contain both their stored ID and public URL, and are also returned as MCP resource links. Each step's `picture`, if any, is a full URL too (resolved server-side, unlike the REST/website representation where a step's `picture` is a bare filename).
- `create_recipe`: requires a complete recipe. `category` (`food` or `diy`) is optional and defaults to `food`; `kind` is required for `food` and ignored for `diy`. Pictures cannot be uploaded through this tool, at the recipe level or per step; attach them via the website. Any `picture` sent on a step is ignored.
- `update_recipe`: requires `recipe_id`; all other recipe fields are true patch fields. Optional `keep_picture_ids` gives the ordered existing pictures to retain, or removes them all with an empty list. New pictures cannot be uploaded through this tool, at the recipe level or per step; attach them via the website. A step's `picture` may only be left empty or set to a filename that already belongs to one of the recipe's existing steps (i.e. keep or reassign an existing step picture) — any other value is rejected.

There is intentionally no MCP delete tool. Recipe deletion remains available through the website/REST API.

MCP tool calls carry structured JSON, which makes uploading binary picture data through them impractical (it has to be inlined as base64), so picture attachment is REST/website-only — see below.

Recipes must have a nonblank title, quantity of at least one, nonnegative times, at least one named ingredient, and at least one step with a description. `category` is one of `food` (default) or `diy`; a supported `kind` is required only when `category` is `food` and is ignored otherwise. A step's optional `timer_minutes` (a countdown shown to viewers on that step) must be nonnegative. Incomplete recipes are rejected rather than saved as drafts.

## Localization

`list_my_recipes` and `get_my_recipe` accept only one locale. Translations are produced when a recipe is created or updated, never on read: the stored translation is returned when it is current (its source hash still matches the canonical recipe), otherwise the canonical recipe is returned. The returned `locale` and `source_locale` say what was actually served. A just-created or just-edited recipe is translated by a background worker, so its non-source locales briefly fall back to canonical until that completes; an admin can force a retranslate via `POST /api/v1/recipes/{id}/retranslate`.

## REST picture creation/update

REST recipe creation and patch now accept either ordinary `application/json` (no newly uploaded pictures) or `multipart/form-data`. Multipart requests contain a `recipe` field with the JSON recipe/patch and zero or more repeated `pictures` file fields. On patch, omitted `keep_picture_ids` leaves existing pictures unchanged, while an empty array removes all of them. The old standalone recipe-picture upload/delete endpoints have been removed; public picture GET remains at `/api/v1/recipe-pictures/{id}`.

A single picture per step is uploaded the same way, via a `step_picture_<index>` file field where `<index>` is that step's position in the request's `steps` array (e.g. `step_picture_0` for the first step). A fresh upload always wins over whatever `steps[index].picture` says in the JSON body. Without a matching upload, a step's `picture` is kept as-is on create (ignored — nothing exists yet) or, on patch, must be empty or match a filename already on one of the recipe's existing steps; any other value is rejected the same way an unknown `keep_picture_ids` entry is.

Each uploaded picture is validated the same way regardless of caller or whether it's recipe- or step-level: JPEG and PNG are accepted, the decoded total across a request (recipe pictures and step pictures combined) is limited to `RECIPES_MCP_MAXDECODEDPICTUREBYTES` (default 64 MiB, shared with the recipe service), each image is limited to 40 megapixels and 16,384 pixels on either axis, and content/declared media type/filename extension must all agree. Images are auto-oriented and re-encoded in their original format, which strips EXIF/GPS metadata. Zero pictures is valid.
