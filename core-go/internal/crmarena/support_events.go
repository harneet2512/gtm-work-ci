package crmarena

import (
	"html"
	"time"
)

// supportCasePayload is source_payloads.v1.json#/$defs/supportCase.
type supportCasePayload struct {
	Kind            string     `json:"kind"`
	CaseID          string     `json:"case_id"`
	AccountRecordID string     `json:"account_record_id"`
	ContactRecordID *string    `json:"contact_record_id"`
	OwnerEmail      *string    `json:"owner_email"`
	Subject         string     `json:"subject"`
	Description     *string    `json:"description"`
	Priority        *string    `json:"priority"`
	Origin          *string    `json:"origin"`
	OpenedAt        time.Time  `json:"opened_at"`
	ClosedAt        *time.Time `json:"closed_at"`
}

// chatPayload is source_payloads.v1.json#/$defs/chatTranscript.
type chatPayload struct {
	Kind            string    `json:"kind"`
	TranscriptID    string    `json:"transcript_id"`
	CaseID          *string   `json:"case_id"`
	AccountRecordID string    `json:"account_record_id"`
	ContactRecordID *string   `json:"contact_record_id"`
	OwnerEmail      *string   `json:"owner_email"`
	EndedAt         time.Time `json:"ended_at"`
	BodyText        string    `json:"body_text"`
}

func caseRef(id string) string { return "case:" + id }
func chatRef(id string) string { return "chat:" + id }

// repEmail is the rep's tenant address, or nil when no rep owns the record.
func (b *builder) repEmail(userID string) *string {
	if a, ok := b.reps.ByUser(userID); ok {
		return &a
	}
	return nil
}

// cases: "opened" at CreatedDate without the close date (it is not known yet), "closed" at ClosedDate.
func (b *builder) cases() error {
	for _, c := range b.snap.Cases {
		opened, err := parseDateTime("Case.CreatedDate", c.CreatedDate)
		if err != nil {
			return err
		}
		p := supportCasePayload{Kind: "support_case", CaseID: caseRef(c.ID), AccountRecordID: accountRef(c.AccountID),
			ContactRecordID: contactPtr(c.ContactID), OwnerEmail: b.repEmail(c.OwnerID), Subject: c.Subject,
			Description: strPtr(c.Description), Priority: strPtr(c.Priority), Origin: strPtr(c.Origin), OpenedAt: opened}
		if err := b.add("support", p.CaseID, "opened", opened, p, c.AccountID, "", phaseActivity); err != nil {
			return err
		}
		if c.ClosedDate == "" {
			continue
		}
		closed, err := parseDateTime("Case.ClosedDate", c.ClosedDate)
		if err != nil {
			return err
		}
		p.ClosedAt = &closed
		if err := b.add("support", p.CaseID, "closed", closed, p, c.AccountID, "", phaseActivity); err != nil {
			return err
		}
	}
	return nil
}

// chats: the transcript body is HTML-escaped in the source ("&#39;"); it is delivered as plain text.
// The contact comes from the chat's case (the transcript's own ContactId is empty in the source).
func (b *builder) chats() error {
	for _, ch := range b.snap.Chats {
		ended, err := parseDateTime("LiveChatTranscript.EndTime", ch.EndTime)
		if err != nil {
			return err
		}
		p := chatPayload{Kind: "chat_transcript", TranscriptID: chatRef(ch.ID), AccountRecordID: accountRef(ch.AccountID),
			OwnerEmail: b.repEmail(ch.OwnerID), EndedAt: ended, BodyText: html.UnescapeString(ch.Body)}
		if ch.CaseID != "" {
			p.CaseID = strPtr(caseRef(ch.CaseID))
			p.ContactRecordID = contactPtr(b.idx.cases[ch.CaseID].ContactID)
		}
		if err := b.add("support", p.TranscriptID, "ended", ended, p, ch.AccountID, "", phaseActivity); err != nil {
			return err
		}
	}
	return nil
}

func contactPtr(id string) *string {
	if id == "" {
		return nil
	}
	return strPtr(contactRef(id))
}
