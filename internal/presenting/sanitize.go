package presenting

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The audience sees the slides change; it must never hear about it. Sanitize
// removes the sentences of a spoken line that narrate the presentation itself
// ("Here is the second slide.", "The first point builds in now.", "Let's move
// on to the next slide.") and stage directions ("[pause]", "(click)"), while
// leaving content alone ("Sales moved up 12%.", "Next quarter we expect...").
//
// It works sentence by sentence. A sentence goes when it matches a strong
// pattern (an unambiguous reference to slides, builds, clicks, the screen or
// navigation). A weak pattern ("And finally the second point", "Here is the
// chart") removes it only when little else is left once the phrase is taken
// out, so "Here is what the data says: churn halved." stays.

var (
	// Stage directions inside brackets, and *pauses*-style actions.
	bracketDirection = regexp.MustCompile(`\[[^\]]{0,80}\]`)
	parenDirection   = regexp.MustCompile(`(?i)\(\s*(?:pause|beat|click|next|advance|transition|build|slide|wait|smile|gesture|point|breathe)[^)]{0,60}\)`)
	starDirection    = regexp.MustCompile(`(?i)\*\s*(?:pause|pauses|beat|click|clicks|next|advance|advances|smile|smiles|gesture|gestures|points|wait|waits|breathe|breathes)[^*]{0,40}\*`)
	markdownMarks    = regexp.MustCompile("\\*\\*|__|`")

	// Openers that only signal moving along; stripped from a sentence's start.
	leadingFiller = regexp.MustCompile(`(?i)^(?:and so|and now|so now|and|so|okay|ok|alright|all right|right|now|next|moving on|with that|without further ado|on to the next one)\s*[,:]\s*`)

	strongMeta = compile(
		// Slides themselves.
		`\bslides?\s*(?:number\s*|no\.?\s*|#\s*)?\d+\b`,
		`\b(?:this|the|next|previous|last|final|following|current|that|first|second|third|fourth|fifth|another|one more|new|our|each|every)\s+slides?\b`,
		`\bslides?\s+(?:shows?|covers?|is about|are about|presents?|says|has|contains|lists|summari[sz]es|walks|explains|highlights|illustrates|focuses)\b`,
		`\bslide\s*(?:deck|show)\b`,
		// Builds, reveals and animations.
		`\b(?:build|build step|animation|transition)s?\s+(?:in|out|on|now|here|appears?|starts?|plays?|happens?)\b`,
		`\b(?:builds?|fades?|flies|slides?|pops?|animates?|zooms?|wipes?|types?)\s+(?:in|into view|onto the screen|on screen|up on screen)(?:\s+(?:now|here|next|too|as well))?\s*(?:[.,!;:]|$)`,
		`\b(?:appears?|appearing|shows? up|showing up|comes? up|coming up|revealed|reveals?|revealing)\s+(?:(?:now|next)\s*(?:[.,!;:]|$)|on (?:the )?screen)`,
		`\bbuild\s+steps?\b`,
		`\banimation\s+(?:plays|starts|shows|runs|now|here)\b`,
		`\banimat(?:es|ing)\s+(?:in|on|out)\b`,
		`\bbullet\s*(?:point|points|list)\b`,
		`\b(?:this|the|next|first|second|third|last|final|another|each|every|that)\s+bullets?\b`,
		`\bbullets?\s+(?:appears?|builds?|comes?|shows?)\b`,
		// The screen.
		`\b(?:on|onto)\s+(?:the\s+|your\s+|this\s+)?screens?\b`,
		`\bas you(?:'ll|\s+will)?\s+(?:can\s+)?see\b`,
		`\byou\s+can\s+see\s+(?:here|on|now)\b`,
		// Navigation.
		`^(?:and\s+|so\s+|now\s+)?(?:moving|let's move|let us move|we'll move|we will move|i'll move)\s+on\b`,
		`\blet'?s\s+move\s+on\b`,
		`\bmove\s+on\s+to\s+(?:the\s+)?(?:next|following)\b`,
		`\bnext\s+up\b`,
		`\bon\s+to\s+the\s+next\b`,
		`\b(?:that|which|this)\s+brings\s+us\s+to\b`,
		`\b(?:go|move|turn|jump|switch|skip|head|proceed|advance|flip|get|come|continue|transition)\s+(?:on\s+|ahead\s+|forward\s+|back\s+|over\s+)?to\s+(?:the\s+)?(?:next|following|previous|last|final|second|third|another|our next)(?:\s+(?:slide|point|item|topic|section|part|one|chart|graph|page|screen|bullet|step)s?\b|\s*(?:[.,!;:]|$))`,
		`\b(?:go|move|turn|jump|switch|skip|head|flip|get|come|continue)\s+(?:on\s+|ahead\s+|forward\s+|back\s+|over\s+)?to\s+slide\b`,
		`\blet'?s\s+(?:go|move|jump|head|skip|flip|advance)\s+(?:on|ahead|forward|back)\b`,
		`\bgo(?:ing)?\s+back\s+to\s+the\s+(?:previous|last)\b`,
		// Clicking.
		`\b(?:i|we|you|let me|let's|i'll|we'll|i will|we will)\s+(?:now\s+|just\s+|can\s+)?click\b`,
		`\b(?:next|another|one more|my next|the next)\s+click\b`,
		`\b(?:with|on)\s+(?:a|the|this|my|one|each)\s+click\b`,
		`\bclick(?:ing)?\s+(?:to|through to|forward|ahead|next)\b`,
		// The presentation describing itself.
		`\b(?:the|this|our)\s+(?:presentation|deck|slideshow|slide show)\s+(?:now\s+)?(?:moves|continues|advances|goes|jumps|will now|is now|ends|starts|begins|switches|shows)\b`,
		`\bend\s+of\s+(?:the|this|our)\s+(?:presentation|deck|slideshow)\b`,
		`\bhere(?:'s|\s+is|\s+are|\s+comes)\s+(?:the\s+|our\s+)?(?:next|first|second|third|fourth|last|final|another)\b`,
		`\bstage\s+directions?\b`,
	)

	weakMeta = compile(
		`^(?:and\s+)?(?:finally|lastly|then|also|now|next)?\s*,?\s*(?:the\s+|our\s+|my\s+|a\s+)?(?:first|second|third|fourth|fifth|next|last|final|another|one more)\s+(?:point|item|one|thing|topic|part|idea)\b`,
		`^(?:and\s+)?(?:here(?:'s|\s+is|\s+are|\s+we\s+have|\s+we\s+go)|there\s+(?:it\s+is|we\s+go))(?:\s+(?:the|our|a|an|this|some))?(?:\s+\w+)?\b`,
		`^(?:and\s+)?(?:finally|lastly|next|now|then|to\s+(?:start|begin|finish|wrap\s+up|close|conclude))\b`,
		`\b(?:let'?s|let\s+us)\s+(?:look|take\s+a\s+look|turn|see|start|begin|continue|move|go)\b`,
		`\b(?:now|next)\s+we\s+(?:have|see|look)\b`,
		`\b(?:appears?|appearing|shows?\s+up|comes?\s+in)\b`,
	)

	abbrevs = map[string]bool{"e.g": true, "i.e": true, "mr": true, "mrs": true, "ms": true, "dr": true, "vs": true, "etc": true,
		"approx": true, "st": true, "inc": true, "ltd": true, "co": true, "no": true, "fig": true, "u.s": true, "u.k": true, "jr": true, "sr": true}
)

func compile(ps ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(ps))
	for i, p := range ps {
		out[i] = regexp.MustCompile(`(?i)` + p)
	}
	return out
}

// Sanitize returns text without its meta-narration and stage directions.
// Blank-line paragraph breaks are kept; it returns "" when nothing is left.
func Sanitize(text string) string {
	var paras []string
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if p = sanitizeParagraph(p); p != "" {
			paras = append(paras, p)
		}
	}
	return strings.Join(paras, "\n\n")
}

func sanitizeParagraph(p string) string {
	p = bracketDirection.ReplaceAllString(p, " ")
	p = parenDirection.ReplaceAllString(p, " ")
	p = starDirection.ReplaceAllString(p, " ")
	p = markdownMarks.ReplaceAllString(p, "")
	var kept []string
	for _, line := range strings.Split(p, "\n") {
		for _, s := range sentences(line) {
			if s = cleanSentence(s); s != "" {
				kept = append(kept, s)
			}
		}
	}
	return strings.Join(kept, " ")
}

// cleanSentence strips filler openers and returns "" for a meta sentence.
func cleanSentence(s string) string {
	s = strings.TrimSpace(s)
	for {
		t := leadingFiller.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = capitalize(t)
	}
	if !hasWords(s) {
		return ""
	}
	for _, re := range strongMeta {
		if re.MatchString(s) {
			return ""
		}
	}
	for _, re := range weakMeta {
		if loc := re.FindStringIndex(s); loc != nil {
			rest := s[:loc[0]] + " " + s[loc[1]:]
			if len(strings.FieldsFunc(rest, notWordRune)) < 2 {
				return ""
			}
		}
	}
	return s
}

func notWordRune(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

func hasWords(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !notWordRune(r) }) >= 0
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || !unicode.IsLower(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// sentences splits a line after ., ! or ? (and closing quotes or brackets)
// followed by a space, except after common abbreviations and initials, so
// "12.5%" and "e.g. this" stay whole.
func sentences(line string) []string {
	var out []string
	start := 0
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '.' && rs[i] != '!' && rs[i] != '?' && rs[i] != '…' {
			continue
		}
		j := i + 1
		for j < len(rs) && strings.ContainsRune(`.!?…"'”’)]`, rs[j]) {
			j++
		}
		if j < len(rs) && !unicode.IsSpace(rs[j]) {
			continue
		}
		if rs[i] == '.' && abbreviation(rs[start:i]) {
			continue
		}
		out = append(out, string(rs[start:j]))
		start = j
		i = j - 1
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

func abbreviation(before []rune) bool {
	k := len(before)
	for k > 0 && !unicode.IsSpace(before[k-1]) {
		k--
	}
	w := strings.ToLower(strings.TrimLeft(string(before[k:]), `("'“‘`))
	if abbrevs[w] {
		return true
	}
	r := []rune(w)
	return len(r) == 1 && unicode.IsLetter(r[0]) // an initial: "J. Smith"
}
