"""/v1/revise: the revision planner. One bounded step: the failed candidate plus the eval feedback in, the same
candidate with a corrected action out. It pulls the people of the account once (no model-driven tool loop), so a
corrected recipient list can only name people core returned; evidence and knowledge references stay the
candidate's own."""
from __future__ import annotations

import logging

from ..draft.core_client import CoreContextClient, ToolParams
from ..draft.deadline import Deadline
from ..draft.grounding import PullLog
from ..errors import InvalidStrategiesError
from ..llm.provider import LLMProvider
from ..models.draft import FinishedArtifact, Recipient
from ..models.strategies import ReviseRequest, ReviseResponse
from .prompt import build_reviser_system_prompt, build_reviser_user_prompt
from .schemas import REVISION_SCHEMA, REVISION_SCHEMA_NAME, Revision, parse_model_output
from .service import preview_of

log = logging.getLogger(__name__)
PEOPLE_LIMIT = 20


def revise_candidate(request: ReviseRequest, provider: LLMProvider, core: CoreContextClient, *,
                     deadline: Deadline) -> ReviseResponse:
    packet = core.pull("people", ToolParams(limit=PEOPLE_LIMIT), timeout_s=deadline.check("pulling people"))
    pulls = PullLog().record(packet)
    feedback = [f.model_dump(mode="json") for f in request.feedback]
    deadline.check("the revision step")
    result = provider.complete_json(system=build_reviser_system_prompt(), schema=REVISION_SCHEMA,
                                    user=build_reviser_user_prompt(request.candidate, feedback, packet),
                                    schema_name=REVISION_SCHEMA_NAME)
    revision = parse_model_output(Revision, result.content, "revision")
    pulls.require_known_people((p.person_id for p in (*revision.to, *revision.cc)), "recipient")
    art = revision.artifact
    artifact = FinishedArtifact(channel=art.channel, subject=art.subject, body=art.body, attachments=art.attachments)
    try:
        revised = request.candidate.model_copy(update={
            "to": tuple(Recipient(person_id=p.person_id, role="to", why=p.why) for p in revision.to),
            "cc": tuple(Recipient(person_id=p.person_id, role="cc", why=p.why) for p in revision.cc),
            "subject": artifact.subject, "full_action_artifact": artifact, "rationale": revision.rationale,
            "preview": preview_of(artifact.body, request.candidate.description),
            "draft_index": None, "eval_bundle_ref": None})
        revised = type(revised).model_validate(revised.model_dump(mode="json"))  # re-run the contract rules
    except ValueError as exc:
        raise InvalidStrategiesError(f"the revision violates the candidate contract: "
                                     f"{str(exc).splitlines()[-1][:160]}") from None
    log.info("candidate revised", extra={"run_id": request.run_id, "strategy_type": revised.strategy_type})
    return ReviseResponse(candidate=revised, model=result.model)
