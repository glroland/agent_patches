// Package sanitize guards tool outputs against prompt injection before they
// are inserted into the LLM's message context.
//
// Tool outputs are the primary injection surface: they contain data from the
// managed system (log lines, package descriptions, command output) that an
// attacker could craft to hijack the agent's behaviour. The sanitizer:
//
//  1. Strips control characters and Unicode steganography used to conceal
//     injected text (zero-width spaces, bidirectional overrides, etc.).
//  2. Redacts known prompt-injection patterns (fake system-message delimiters,
//     constraint-bypass phrases, system-prompt exfiltration probes).
//  3. Truncates excessively long outputs to prevent context-flooding attacks
//     that bury the system prompt or crowd out earlier conversation turns.
//
// The sanitizer is deliberately conservative: it targets constructs that are
// structurally impossible in legitimate sysadmin output so the false-positive
// rate stays near zero while catching real injection attempts.
package sanitize

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const (
	// maxOutputBytes caps a single tool output before it is added to the LLM
	// context. 16 KB (~4-5k tokens) covers realistic sysadmin tool responses
	// while guaranteeing a multi-iteration run fits the upstream model's
	// 32768-token context window; anything larger is almost certainly
	// context flooding.
	maxOutputBytes = 16 * 1024

	redactionMarker = "[REDACTED: potential prompt injection]"
)

// injectionPatterns are compiled once at startup. Each matches a construct
// that has no legitimate meaning in sysadmin tool output but is a well-known
// prompt injection or jailbreak technique.
var injectionPatterns = []*regexp.Regexp{
	// ── Structural model-format injections ───────────────────────────────────
	// Fake <system>…</system> blocks used to impersonate system messages.
	regexp.MustCompile(`(?is)<\s*system\s*>.*?<\s*/\s*system\s*>`),
	// LLaMA / Alpaca instruction-tuning delimiters.
	regexp.MustCompile(`(?i)\[INST\]`),
	regexp.MustCompile(`(?i)\[/INST\]`),
	regexp.MustCompile(`(?i)<<SYS>>`),
	regexp.MustCompile(`(?i)<</SYS>>`),
	// ChatML / generic heading-style injection.
	regexp.MustCompile(`(?i)<\|im_start\|>`),
	regexp.MustCompile(`(?i)<\|im_end\|>`),
	regexp.MustCompile(`(?i)###\s*SYSTEM\s*###`),
	regexp.MustCompile(`(?i)###\s*INSTRUCTION\s*###`),
	regexp.MustCompile(`(?i)\[SYSTEM\]`),

	// ── Constraint-bypass phrases ─────────────────────────────────────────────
	// "ignore / disregard all previous instructions"
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above|earlier|the\s+above)\s+(instructions?|prompts?|rules?|constraints?|directives?)`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|rules?|constraints?|directives?)`),
	regexp.MustCompile(`(?i)forget\s+(everything|all)\s+(above|before|prior|previous)`),
	regexp.MustCompile(`(?i)override\s+(your\s+)?(safety\s+)?(instructions?|directives?|constraints?|programming|rules?|guidelines?)`),

	// ── Role / persona injection ──────────────────────────────────────────────
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a\s+)?(new\s+)?(different\s+)?(unrestricted|uncensored|evil|jailbroken)`),
	regexp.MustCompile(`(?i)your\s+(new\s+)?(true\s+)?(purpose|goal|mission|prime\s+directive)\s+is\s+to`),
	// DAN / "do anything now" jailbreak families.
	regexp.MustCompile(`(?i)\bDAN\s+mode\b`),
	regexp.MustCompile(`(?i)\bdo\s+anything\s+now\b`),
	regexp.MustCompile(`(?i)\bjailbreak(ed)?\b`),

	// ── System-prompt / credential exfiltration probes ───────────────────────
	regexp.MustCompile(`(?i)(print|output|reveal|show|display|leak|send|exfil(?:trate)?)\s+(me\s+)?(the\s+)?(full\s+)?(system\s+prompt|api\s+key|secret\s+key|private\s+key|credentials?)`),
	regexp.MustCompile(`(?i)(what\s+is|tell\s+me)\s+(your\s+)?(system\s+prompt|api\s+key|secret\s+key)`),
	regexp.MustCompile(`(?i)(base64|hex|rot13)\s+(en|de)code\s+(the\s+)?(system\s+prompt|instructions?)`),
}

// ToolOutput sanitises a tool-result string before it is appended to the LLM
// message context. It returns the sanitised string and the number of distinct
// sanitisation events that fired (0 = nothing found). The caller should log
// or record a trace attribute when the count is non-zero.
func ToolOutput(s string) (string, int) {
	events := 0

	// 1. Strip control characters and Unicode steganography.
	cleaned, controlsFound := stripControls(s)
	if controlsFound {
		events++
	}

	// 2. Redact injection patterns.
	for _, re := range injectionPatterns {
		if re.MatchString(cleaned) {
			events++
			cleaned = re.ReplaceAllString(cleaned, redactionMarker)
		}
	}

	// 3. Truncate to prevent context flooding.
	if len(cleaned) > maxOutputBytes {
		events++
		cleaned = cleaned[:maxOutputBytes] +
			fmt.Sprintf("\n\n... [output truncated at %d bytes]", maxOutputBytes)
	}

	if events > 0 {
		slog.Warn("sanitize: tool output contained potentially injected content",
			"events", events,
			"original_bytes", len(s),
			"sanitized_bytes", len(cleaned),
		)
	}

	return cleaned, events
}

// stripControls removes ASCII control characters (except \t \n \r) and Unicode
// zero-width / directional-override code points commonly used to hide injection
// payloads inside otherwise innocent-looking text. Returns the cleaned string
// and whether anything was removed.
func stripControls(s string) (string, bool) {
	found := false
	cleaned := strings.Map(func(r rune) rune {
		// Preserve normal whitespace.
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		// Drop ASCII control characters.
		if r < 0x20 || r == 0x7F {
			found = true
			return -1
		}
		// Drop Unicode zero-width and bidirectional-override characters.
		switch r {
		case 0xFEFF, // BOM / zero-width no-break space
			0x200B, 0x200C, 0x200D, // zero-width (non-)joiners
			0x2028, 0x2029, // line separator, paragraph separator
			0x202A, 0x202B, 0x202C, 0x202D, 0x202E: // bidirectional overrides
			found = true
			return -1
		}
		return r
	}, s)
	return cleaned, found
}
