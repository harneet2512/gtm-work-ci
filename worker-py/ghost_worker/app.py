"""FastAPI app: GET /healthz, POST /v1/extract and POST /v1/draft (contracts/openapi/worker.yaml)."""
from __future__ import annotations

import logging

import httpx
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from .ask.core_client import AskToolClient
from .ask.loop import run_ask
from .ask.models import AskRequest, AskResponse
from .body_limit import BodySizeLimitMiddleware, BodyTooLarge, too_large_response
from .bucket2.decision_judge import JudgeRequest as DecisionJudgeRequest
from .bucket2.decision_judge import JudgeResponse as DecisionJudgeResponse
from .bucket2.decision_judge import judge as judge_decision
from .bucket2.inference import infer_judgment
from .bucket2.inference_models import InferenceRequest, InferenceResponse
from .delta import label_human_delta
from .draft.core_client import CoreContextClient
from .draft.deadline import Deadline
from .draft.service import draft_action
from .errors import (ConfigError, InvalidRequestError, ProviderUnavailableError, UnsupportedExtractorVersionError,
                     WorkerError)
from .extract import extract_claims
from .llm.breaker import BreakerProvider, CircuitBreaker
from .llm.factory import build_ask_provider, build_extract_provider, build_provider
from .llm.limited import ConcurrencyLimitedProvider
from .llm.provider import LLMProvider
from .llm.usage import MeteredProvider, UsageMeter
from .logging_setup import configure_logging
from .models import ErrorEnvelope, ExtractRequest, ExtractResponse, HealthResponse
from .judges import EvalCatalog, Rubric, load_catalog, load_rubrics
from .models.draft import DraftRequest, DraftResponse
from .models.humandelta import HumanDeltaRequest, HumanDeltaResponse
from .models.strategies import (JudgeRequest, JudgeResponse, ReviseRequest, ReviseResponse, StrategiesRequest,
                                StrategiesResponse)
from .strategies.judge import judge_candidate
from .strategies.revise import revise_candidate
from .strategies.service import generate_strategies
from .settings import Settings

log = logging.getLogger(__name__)

MAX_BODY_BYTES = 1024 * 1024  # 60000 chars of text (<= 4 bytes each) plus activity/known_people headroom
MAX_REPORTED_ERRORS = 10

_ERROR_RESPONSES = {422: {"model": ErrorEnvelope}, 424: {"model": ErrorEnvelope}, 502: {"model": ErrorEnvelope}}


def _envelope(status: int, code: str, message: str) -> JSONResponse:
    return JSONResponse(status_code=status, content=ErrorEnvelope.of(code, message).model_dump())


def _validation_message(exc: RequestValidationError) -> str:
    """Field locations and rule text only: never echo the submitted values (they may hold deal text)."""
    parts = [f"{'.'.join(str(p) for p in e['loc'] if p != 'body') or 'body'}: {e['msg']}"
             for e in exc.errors()[:MAX_REPORTED_ERRORS]]
    return "invalid request: " + "; ".join(parts)


def _register_error_handlers(app: FastAPI) -> None:
    @app.exception_handler(BodyTooLarge)
    async def on_body_too_large(_: Request, __: BodyTooLarge) -> JSONResponse:
        return too_large_response(MAX_BODY_BYTES)

    @app.exception_handler(ConfigError)
    async def on_config_error(_: Request, exc: ConfigError) -> JSONResponse:
        log.error("configuration error", extra={"error_type": "ConfigError"})
        return _envelope(500, "internal_error", "internal error")

    @app.exception_handler(RequestValidationError)
    async def on_validation_error(_: Request, exc: RequestValidationError) -> JSONResponse:
        return _envelope(422, "invalid_request", _validation_message(exc))

    @app.exception_handler(UnsupportedExtractorVersionError)
    async def on_unsupported_version(_: Request, exc: UnsupportedExtractorVersionError) -> JSONResponse:
        return _envelope(422, "invalid_request", "unsupported extractor_version")

    @app.exception_handler(InvalidRequestError)
    async def on_invalid_request(_: Request, exc: InvalidRequestError) -> JSONResponse:
        return _envelope(422, "invalid_request", str(exc))

    @app.exception_handler(ProviderUnavailableError)
    async def on_provider_unavailable(_: Request, exc: ProviderUnavailableError) -> JSONResponse:
        """Non-retryable (credits, quota, auth, open breaker): 424 so core never retries it (HAR-135)."""
        log.error("provider unavailable (non-retryable)", extra={"error_type": type(exc).__name__})
        return _envelope(424, exc.code, exc.public_message)

    @app.exception_handler(WorkerError)
    async def on_worker_error(_: Request, exc: WorkerError) -> JSONResponse:
        """Every other expected failure is a 502 whose code says which part failed (never its detail)."""
        log.error("worker error", extra={"error_type": type(exc).__name__, "detail": str(exc)})
        return _envelope(502, exc.code, exc.public_message)

    @app.exception_handler(Exception)
    async def on_unexpected(_: Request, exc: Exception) -> JSONResponse:
        log.error("unhandled error", extra={"error_type": type(exc).__name__})
        return _envelope(500, "internal_error", "internal error")


def _register_routes(app: FastAPI, cfg: Settings, extract_llm: LLMProvider, draft_llm: LLMProvider,
                     judge_llm: LLMProvider, ask_llm: LLMProvider, core_transport: httpx.BaseTransport | None) -> None:
    catalog: EvalCatalog = load_catalog()
    rubrics: dict[str, Rubric] = dict(load_rubrics(catalog))

    @app.get("/healthz", response_model=HealthResponse, operation_id="health")
    def health() -> HealthResponse:
        return HealthResponse(status="ok", model=cfg.ghost_model, llm_mode=cfg.ghost_llm_mode)

    @app.post("/v1/extract", response_model=ExtractResponse, operation_id="extractClaims",
              responses=_ERROR_RESPONSES)
    def extract(request: ExtractRequest) -> ExtractResponse:
        return extract_claims(request, extract_llm)

    @app.post("/v1/draft", response_model=DraftResponse, response_model_exclude_none=True,
              operation_id="draftAction", responses=_ERROR_RESPONSES)
    def draft(request: DraftRequest) -> DraftResponse:
        deadline = Deadline(cfg.draft_deadline_s)
        meter = UsageMeter()
        with CoreContextClient(cfg.core_url, request.run_token, timeout_s=cfg.core_timeout_s,
                               max_bytes=cfg.core_max_bytes, transport=core_transport) as core:
            response = draft_action(request, MeteredProvider(draft_llm, meter), core, deadline=deadline)
        return response.model_copy(update={"usage": meter.summary()})

    @app.post("/v1/strategies", response_model=StrategiesResponse, operation_id="generateStrategies",
              responses=_ERROR_RESPONSES)
    def strategies(request: StrategiesRequest) -> StrategiesResponse:
        meter = UsageMeter()
        with CoreContextClient(cfg.core_url, request.run_token, timeout_s=cfg.core_timeout_s,
                               max_bytes=cfg.core_max_bytes, transport=core_transport) as core:
            response = generate_strategies(request, MeteredProvider(draft_llm, meter), core,
                                           deadline=Deadline(cfg.strategies_deadline_s))
        return response.model_copy(update={"usage": meter.summary()})

    @app.post("/v1/judge", response_model=JudgeResponse, operation_id="judgeCandidate", responses=_ERROR_RESPONSES)
    def judge(request: JudgeRequest) -> JudgeResponse:
        meter = UsageMeter()
        with CoreContextClient(cfg.core_url, request.run_token, timeout_s=cfg.core_timeout_s,
                               max_bytes=cfg.core_max_bytes, transport=core_transport) as core:
            response = judge_candidate(request, MeteredProvider(judge_llm, meter), core,
                                       deadline=Deadline(cfg.judge_deadline_s), rubrics=rubrics, catalog=catalog)
        return response.model_copy(update={"usage": meter.summary()})

    @app.post("/v1/human-delta", response_model=HumanDeltaResponse, operation_id="labelHumanDelta",
              responses=_ERROR_RESPONSES)
    def human_delta(request: HumanDeltaRequest) -> HumanDeltaResponse:
        # Self-contained by contract (no run_token, no /internal/ctx): the judge provider labels the edit.
        meter = UsageMeter()
        response = label_human_delta(request, MeteredProvider(judge_llm, meter))
        return response.model_copy(update={"usage": meter.summary()})

    @app.post("/v1/judgment-inference", response_model=InferenceResponse, operation_id="inferJudgment",
              responses=_ERROR_RESPONSES)
    def judgment_inference(request: InferenceRequest) -> InferenceResponse:
        # Self-contained: the run_token authorises the call but nothing is pulled from core.
        return infer_judgment(request, MeteredProvider(judge_llm, UsageMeter()))

    @app.post("/v1/decision-judge", response_model=DecisionJudgeResponse, operation_id="judgeDecision",
              responses=_ERROR_RESPONSES)
    def decision_judge(request: DecisionJudgeRequest) -> DecisionJudgeResponse:
        # Self-contained: core sends the stored objects and the evidence ids a verdict may cite.
        return judge_decision(request, MeteredProvider(judge_llm, UsageMeter()))

    @app.post("/v1/ask", response_model=AskResponse, response_model_exclude_none=True, operation_id="askCliff",
              responses=_ERROR_RESPONSES)
    def ask(request: AskRequest) -> AskResponse:
        # Normal provider path (same model, concurrency cap and spend guard). The tools are core's, over the ask token.
        meter = UsageMeter()
        deadline = Deadline(min(request.deadline_s, cfg.ask_deadline_s))
        with AskToolClient(cfg.core_url, request.ask_token, timeout_s=cfg.core_timeout_s,
                           max_bytes=cfg.core_max_bytes, transport=core_transport) as tools:
            response = run_ask(request, MeteredProvider(ask_llm, meter), tools, deadline=deadline)
        return response.model_copy(update={"usage": meter.summary()})

    @app.post("/v1/revise", response_model=ReviseResponse, operation_id="reviseCandidate",
              responses=_ERROR_RESPONSES)
    def revise(request: ReviseRequest) -> ReviseResponse:
        meter = UsageMeter()
        with CoreContextClient(cfg.core_url, request.run_token, timeout_s=cfg.core_timeout_s,
                               max_bytes=cfg.core_max_bytes, transport=core_transport) as core:
            response = revise_candidate(request, MeteredProvider(draft_llm, meter), core,
                                        deadline=Deadline(cfg.draft_deadline_s))
        return response.model_copy(update={"usage": meter.summary()})


def create_app(settings: Settings | None = None, provider: LLMProvider | None = None,
               core_transport: httpx.BaseTransport | None = None,
               breaker: CircuitBreaker | None = None) -> FastAPI:
    """`provider` and `core_transport` are injectable for tests (one provider then serves both endpoints);
    otherwise providers are built from `settings` (fails fast on bad config): /v1/extract with
    `extract_deadline_s`, /v1/draft with `llm_deadline_s`, sharing one concurrency cap. Core is reached over
    HTTP at `settings.core_url`."""
    configure_logging()
    cfg = settings or Settings()
    breaker = breaker or CircuitBreaker(cfg.provider_breaker_threshold, cfg.provider_breaker_cooldown_s)
    draft_llm = ConcurrencyLimitedProvider(
        BreakerProvider(provider if provider is not None else build_provider(cfg), breaker),
        max_concurrent=cfg.max_concurrency, acquire_timeout_s=cfg.llm_deadline_s)
    extract_llm = draft_llm.share(
        BreakerProvider(provider if provider is not None else build_extract_provider(cfg), breaker),
        acquire_timeout_s=cfg.extract_deadline_s)
    judge_llm = draft_llm.share(
        BreakerProvider(provider if provider is not None else build_provider(cfg), breaker),
        acquire_timeout_s=cfg.judge_deadline_s)
    # Ask Cliff: its own limiter (a separate, small slot pool), its own cache directory, the shared breaker and spend guard.
    ask_llm = ConcurrencyLimitedProvider(
        BreakerProvider(provider if provider is not None else build_ask_provider(cfg), breaker),
        max_concurrent=cfg.ask_max_concurrency, acquire_timeout_s=cfg.ask_deadline_s)
    app = FastAPI(title="Ghost model worker API", version="0.1.0")
    app.state.ask_provider = ask_llm
    app.state.settings = cfg
    app.state.provider_breaker = breaker
    app.state.extract_provider = extract_llm
    app.state.draft_provider = draft_llm
    app.add_middleware(BodySizeLimitMiddleware, max_bytes=MAX_BODY_BYTES)
    _register_error_handlers(app)
    _register_routes(app, cfg, extract_llm, draft_llm, judge_llm, ask_llm, core_transport)
    return app
