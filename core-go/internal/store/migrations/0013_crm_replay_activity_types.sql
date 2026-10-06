-- +goose Up
-- HAR-130 (WP31): activity types for the CRMArena-Pro deal-replay records (tasks, quotes, contracts,
-- orders, live-chat transcripts). Must equal common.v1.json#/$defs/activityType.
ALTER TABLE activities DROP CONSTRAINT activities_activity_type_check;
ALTER TABLE activities ADD CONSTRAINT activities_activity_type_check CHECK (activity_type IN (
    'EmailSent', 'EmailReceived', 'EmailReply',
    'CallStarted', 'CallEnded', 'TranscriptReady',
    'MeetingScheduled', 'MeetingAccepted', 'MeetingDeclined', 'MeetingCompleted', 'MeetingParticipantAdded',
    'SlackMessage', 'SlackDecision',
    'CRMFieldChanged', 'CRMNoteAdded', 'OpportunityStageChanged', 'ContactAdded', 'StakeholderRoleChanged',
    'CRMTaskLogged', 'QuoteCreated', 'ContractSigned', 'OrderPlaced',
    'MarketingSignal', 'WebsiteIntent', 'ProductUsageChanged',
    'SupportTicketOpened', 'SupportTicketResolved', 'ChatTranscriptReady', 'DocumentShared', 'EnrichmentUpdated',
    'AgentActionProposed', 'AgentActionExecuted',
    'HumanApproved', 'HumanEdited', 'HumanRejected',
    'CustomerReplied', 'CustomerWentSilent', 'BusinessOutcomeChanged'));

-- +goose Down
-- Fails while activities of the new types exist; delete them first.
ALTER TABLE activities DROP CONSTRAINT activities_activity_type_check;
ALTER TABLE activities ADD CONSTRAINT activities_activity_type_check CHECK (activity_type IN (
    'EmailSent', 'EmailReceived', 'EmailReply',
    'CallStarted', 'CallEnded', 'TranscriptReady',
    'MeetingScheduled', 'MeetingAccepted', 'MeetingDeclined', 'MeetingCompleted', 'MeetingParticipantAdded',
    'SlackMessage', 'SlackDecision',
    'CRMFieldChanged', 'CRMNoteAdded', 'OpportunityStageChanged', 'ContactAdded', 'StakeholderRoleChanged',
    'MarketingSignal', 'WebsiteIntent', 'ProductUsageChanged',
    'SupportTicketOpened', 'SupportTicketResolved', 'DocumentShared', 'EnrichmentUpdated',
    'AgentActionProposed', 'AgentActionExecuted',
    'HumanApproved', 'HumanEdited', 'HumanRejected',
    'CustomerReplied', 'CustomerWentSilent', 'BusinessOutcomeChanged'));
