package slacksurface

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// Slack Block Kit limits (https://docs.slack.dev/reference/block-kit/).
const (
	maxMessageBlocks = 50
	maxModalBlocks   = 100
	maxSectionText   = 3000
	maxFieldText     = 2000
	maxHeaderText    = 150
	maxButtonText    = 75
	maxActionValue   = 2000
	maxActionID      = 255
	maxBlockID       = 255
	maxModalTitle    = 24
	maxMetadata      = 3000
	maxInputInitial  = 3000
	maxURL           = 3000
	maxActionsInBlk  = 25
	maxFields        = 10
	maxConfirmText   = 300
	maxConfirmTitle  = 100
	maxConfirmButton = 30
	maxMessageText   = 40000

	// draftTruncate is the longest email body shown inline in a channel message (HAR-129 / actions.md);
	// the full text is always available in the View full modal. It leaves headroom under the 3,000
	// character section limit for the code fence and the truncation marker.
	draftTruncate = 2800
	// textChunk is the largest text packed into one section when a long text is split.
	textChunk = 2800
)

// Message is a channel message: plain-text fallback plus blocks.
type Message struct {
	Text   string        `json:"text"`
	Blocks []slack.Block `json:"blocks"`
	// Meta, when set, rides in the Slack message metadata of a post or update (HAR-136): it is how a message whose
	// post was interrupted is found again in the channel history. It is not part of the rendered message.
	Meta *MessageMeta `json:"-"`
}

// truncate shortens s to at most max runes, ending with an ellipsis when it cut.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return trimPartialEntity(strings.TrimRight(string(r[:max-1]), " \n")) + "…"
}

// trimPartialEntity drops a half-cut "&amp;", "&lt;" or "&gt;" at the end of s, so truncating text
// that is already escaped never leaves an entity Slack would show literally.
func trimPartialEntity(s string) string {
	if i := strings.LastIndexByte(s, '&'); i >= 0 && len(s)-i <= 4 && !strings.Contains(s[i:], ";") {
		return s[:i]
	}
	return s
}

// splitText cuts s into chunks of at most size runes, preferring line breaks.
func splitText(s string, size int) []string {
	r := []rune(s)
	var out []string
	for len(r) > size {
		cut := size
		for i := size; i > size/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return append(out, string(r))
}

// ValidateBlocks checks marshalled Block Kit JSON against Slack's documented limits. maxBlocks is
// maxMessageBlocks for a message and maxModalBlocks for a modal. It is independent of the builders,
// so it also catches builder mistakes; renderers are tested through it.
func ValidateBlocks(raw []byte, maxBlocks int) error {
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return fmt.Errorf("blocks: %w", err)
	}
	if len(blocks) > maxBlocks {
		return fmt.Errorf("%d blocks exceeds %d", len(blocks), maxBlocks)
	}
	seen := map[string]bool{}
	for i, b := range blocks {
		if err := validateBlock(b, seen); err != nil {
			return fmt.Errorf("block %d (%v): %w", i, b["type"], err)
		}
	}
	return nil
}

func validateBlock(b map[string]any, seen map[string]bool) error {
	if id, _ := b["block_id"].(string); id != "" {
		if len(id) > maxBlockID {
			return fmt.Errorf("block_id longer than %d", maxBlockID)
		}
		if seen[id] {
			return fmt.Errorf("duplicate block_id %q", id)
		}
		seen[id] = true
	}
	if t, ok := b["text"].(map[string]any); ok {
		limit := maxSectionText
		if b["type"] == "header" {
			limit = maxHeaderText
		}
		if err := checkText(t, limit); err != nil {
			return err
		}
	}
	if f, ok := b["fields"].([]any); ok {
		if len(f) > maxFields {
			return fmt.Errorf("%d fields exceeds %d", len(f), maxFields)
		}
		for _, x := range f {
			if m, ok := x.(map[string]any); ok {
				if err := checkText(m, maxFieldText); err != nil {
					return err
				}
			}
		}
	}
	if els, ok := b["elements"].([]any); ok && b["type"] == "actions" && len(els) > maxActionsInBlk {
		return fmt.Errorf("%d actions exceeds %d", len(els), maxActionsInBlk)
	}
	if els, ok := b["elements"].([]any); ok {
		for _, e := range els {
			if m, ok := e.(map[string]any); ok {
				if err := validateElement(m); err != nil {
					return err
				}
			}
		}
	}
	if el, ok := b["element"].(map[string]any); ok {
		return validateElement(el)
	}
	return nil
}

func validateElement(m map[string]any) error {
	if id, _ := m["action_id"].(string); len(id) > maxActionID {
		return fmt.Errorf("action_id longer than %d", maxActionID)
	}
	if v, _ := m["value"].(string); len(v) > maxActionValue {
		return fmt.Errorf("action value longer than %d", maxActionValue)
	}
	if u, _ := m["url"].(string); len(u) > maxURL {
		return fmt.Errorf("url longer than %d", maxURL)
	}
	if iv, _ := m["initial_value"].(string); utf8.RuneCountInString(iv) > maxInputInitial {
		return fmt.Errorf("initial_value longer than %d", maxInputInitial)
	}
	if ml, ok := m["max_length"].(float64); ok {
		if iv, _ := m["initial_value"].(string); float64(utf8.RuneCountInString(iv)) > ml {
			return fmt.Errorf("initial_value longer than its max_length %d", int(ml))
		}
	}
	if c, ok := m["confirm"].(map[string]any); ok {
		if err := validateConfirm(c); err != nil {
			return err
		}
	}
	if t, ok := m["text"].(map[string]any); ok && m["type"] == "button" {
		return checkText(t, maxButtonText)
	}
	return nil
}

func validateConfirm(c map[string]any) error {
	for key, limit := range map[string]int{"title": maxConfirmTitle, "text": maxConfirmText, "confirm": maxConfirmButton, "deny": maxConfirmButton} {
		t, ok := c[key].(map[string]any)
		if !ok {
			return fmt.Errorf("confirm.%s missing", key)
		}
		if err := checkText(t, limit); err != nil {
			return fmt.Errorf("confirm.%s: %w", key, err)
		}
	}
	return nil
}

func checkText(t map[string]any, limit int) error {
	s, _ := t["text"].(string)
	if s == "" {
		return fmt.Errorf("empty text (Slack rejects it)")
	}
	if n := utf8.RuneCountInString(s); n > limit {
		return fmt.Errorf("text of %d characters exceeds %d", n, limit)
	}
	return nil
}

// ValidateMessage checks a Message: block limits, non-empty fallback text.
func ValidateMessage(m Message) error {
	if strings.TrimSpace(m.Text) == "" {
		return fmt.Errorf("message has no fallback text")
	}
	if utf8.RuneCountInString(m.Text) > maxMessageText {
		return fmt.Errorf("fallback text too long")
	}
	raw, err := json.Marshal(m.Blocks)
	if err != nil {
		return err
	}
	return ValidateBlocks(raw, maxMessageBlocks)
}

// ValidateModal checks a modal view request.
func ValidateModal(v slack.ModalViewRequest) error {
	if n := utf8.RuneCountInString(v.Title.Text); n == 0 || n > maxModalTitle {
		return fmt.Errorf("modal title length %d not in 1..%d", n, maxModalTitle)
	}
	if len(v.PrivateMetadata) > maxMetadata {
		return fmt.Errorf("private_metadata longer than %d", maxMetadata)
	}
	raw, err := json.Marshal(v.Blocks.BlockSet)
	if err != nil {
		return err
	}
	return ValidateBlocks(raw, maxModalBlocks)
}
