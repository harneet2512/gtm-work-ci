"""Closed vocabularies mirrored from contracts/schemas (parity is asserted in tests)."""
from __future__ import annotations

from typing import Literal

FieldPath = Literal[
    "stage", "health", "owner", "motion",
    "champion", "champion_status", "economic_buyer", "buying_group.member", "stakeholder_role",
    "blockers", "objections", "decision_criteria", "decision_process",
    "commitment", "next_milestone", "next_meeting", "relationship_risk",
    "product_use_case", "commercial_issue", "delegation", "summary",
]

Role = Literal[
    "champion", "economic_buyer", "technical_evaluator", "security", "legal",
    "executive_sponsor", "user", "influencer", "blocker", "procurement", "unknown",
]

ParticipantRole = Literal[
    "actor", "from", "to", "cc", "bcc", "attendee", "organizer", "speaker", "mentioned", "owner",
]

SourceSystem = Literal[
    "crm", "email", "calendar", "call", "slack", "sales_engagement", "marketing",
    "product", "support", "docs", "enrichment", "agent", "human", "ghost.clock",
]

ActivityType = Literal[
    "EmailSent", "EmailReceived", "EmailReply",
    "CallStarted", "CallEnded", "TranscriptReady",
    "MeetingScheduled", "MeetingAccepted", "MeetingDeclined", "MeetingCompleted", "MeetingParticipantAdded",
    "SlackMessage", "SlackDecision",
    "CRMFieldChanged", "CRMNoteAdded", "OpportunityStageChanged", "ContactAdded", "StakeholderRoleChanged",
    "CRMTaskLogged", "QuoteCreated", "ContractSigned", "OrderPlaced",
    "MarketingSignal", "WebsiteIntent", "ProductUsageChanged",
    "SupportTicketOpened", "SupportTicketResolved", "ChatTranscriptReady", "DocumentShared", "EnrichmentUpdated",
    "AgentActionProposed", "AgentActionExecuted",
    "HumanApproved", "HumanEdited", "HumanRejected",
    "CustomerReplied", "CustomerWentSilent", "BusinessOutcomeChanged",
]

Visibility = Literal["org", "team", "owner_only", "restricted"]
