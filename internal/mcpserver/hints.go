package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Tool hints (MCP annotations). Every tool states all four, so clients don't
// fall back to the protocol's defaults (destructive and open-world).
// Destructive means the call can remove or overwrite content or break links
// already shared; edits stay undoable through restore_version. Open-world
// means the call reaches past Slidekick's own data: a TV on the network, or
// the AI and voice services that narrate a presentation.
func hints(readOnly, destructive, idempotent, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &openWorld}
}

var (
	// readOnly reads Slidekick's own data and changes nothing.
	readOnly = hints(true, false, true, false)
	// adds creates something new each call (a deck, a slide).
	adds = hints(false, false, false, false)
	// sets changes something to a given state; repeating it changes nothing more.
	sets = hints(false, false, true, false)
	// replaces overwrites or removes content; repeating it changes nothing more.
	replaces = hints(false, true, true, false)
	// rewrites overwrites or removes content, and repeating it can change more.
	rewrites = hints(false, true, false, false)
	// reachesOut starts something on a TV or through the AI and voice services.
	reachesOut = hints(false, false, false, true)
	// stopsOutside stops something on a TV; stopping twice is the same as once.
	stopsOutside = hints(false, false, true, true)
)

// returnsVersion ends the description of every tool that saves a new deck version.
const returnsVersion = " Returns the new version and lint findings."
