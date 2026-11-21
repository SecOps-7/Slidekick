# Connecting an agent

Slidekick is an MCP server. Any MCP client can create and edit decks; the human watches at the share link.

## Claude Code (HTTP)

Create a personal agent token on your **Account** page (`skp_...`). The agent acts as you, with your role, so it can never do more than you can, and the decks it creates are yours.

```bash
claude mcp add --transport http slidekick http://<host>:7410/mcp \
  --header "Authorization: Bearer skp_..."
```

The server token (`SLIDEKICK_TOKEN`) also works, but it is the all-powerful emergency key ([self-hosting](self-hosting.md#the-server-token-emergency-key)): use a personal token so edits are attributed and limited to your role.

Decks an agent creates are private: the link `create_deck` and `get_share_url` return carries the view key, so pass that exact link on.

Then ask: *"Use Slidekick to make a 6-slide deck about our Q3 launch, then give me the link."*

### A token for one deck

To let an agent (or a colleague) work on one deck without the server token, open the deck in the editor, choose **Share → Create token** (you need to own the deck or be an admin), and use that `ske_...` token instead. It can edit, present, comment on and export that deck only: other decks answer "this token can only edit deck ...", `create_deck` is refused, and `list_decks` shows just the one deck. The editor shows the token once and the exact `claude mcp add` command. Replace or revoke it from the same menu.

## Claude Desktop, Cursor and other clients (stdio)

Clients that launch a local process can run Slidekick over stdio. Docker keeps it self-contained:

```json
{
  "mcpServers": {
    "slidekick": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-p", "127.0.0.1:7410:7410", "-v", "slidekick-data:/data",
               "ghcr.io/secops-7/slidekick:latest", "stdio", "--addr", ":7410"]
    }
  }
}
```

In stdio mode the viewer also runs at http://localhost:7410, so you can watch while the agent works. Without Docker, use the binary: `"command": "slidekick", "args": ["stdio"]`.

Clients that only speak HTTP but can't send headers can use a server without `SLIDEKICK_TOKEN`, bound to localhost only.

## The built-in AI chat

Editors and admins can also use the **✦ AI** panel in the editor, which drives these same tools with a model an admin connected (see [self-hosting](self-hosting.md#ai-providers-and-the-chat-panel)).

## What the agent gets

| Tool | Use |
| --- | --- |
| `create_deck` | New deck, optionally with every slide in one call |
| `get_deck`, `list_decks` | Read |
| `add_slide`, `update_slide`, `delete_slide`, `move_slide` | Slide edits |
| `patch_deck` | RFC 6902 JSON Patch for precise element edits |
| `set_theme`, `list_themes` | Look and feel |
| `lint_deck` | Overflow, density, contrast, alt text, chart and animation checks |
| `render_slide` | PNG of a slide at any build step (needs Chromium, which the image includes) |
| `upload_asset` | Store an image or video, get a `/files/...` url |
| `export_deck` | `html` (self-contained, offline), `pdf` or `png` (zip) |
| `list_comments`, `resolve_comment` | Requests humans pinned in the editor ("Send to agent") |
| `get_history`, `restore_version` | Version history and undo |
| `get_share_url` | Live link for the user (carries the view key when the deck is private) |
| `focus_slide` | Bring a slide into view in the user's editor and viewer; call it before working on a slide or when the user names one |
| `set_deck_access` | Make a deck public or private again (new decks start private); rotate the key |

Resources: the deck JSON Schema, layout, element, icon, animation and theme catalogues, a design guide for agents, and `slidekick://decks/{id}/comments`. Prompts: `outline_to_deck`, `polish_deck`, `restyle_deck`.

## Working where the user can see it

People usually watch an agent work in the editor or the live viewer. Every edit moves their view to the slide it changed (with **Follow agent** on, the default). Call `focus_slide` before you start on a slide, or when the user mentions one ("fix slide 4"), so they see it before the change lands. Viewers following a presenter aren't moved.

## The loop that works well

1. Read `slidekick://guide` and `slidekick://catalog/layouts` once.
2. `create_deck` with every slide.
3. Fix lint errors and warnings returned by each write.
4. `render_slide` the slides that matter and look at them.
5. Pass `base_version` on writes when a human may be editing; on a conflict, re-read and retry.
6. If a write result shows `openComments`, call `list_comments`, act, and `resolve_comment` with a short reply.
