# One-time "record the demo" (run by the lead, once, after setup-local.ps1). Every model call is made ONCE and stored; after this
# every rehearsal and the live demo replay the stored run at no cost, deterministically.
#
#   .\scripts\demo\record-local.ps1 [-SkipVerify]
#
# It starts the demo with the worker in cache mode (GHOST_LLM_MODE=cache: replay-first, record-on-miss, cassettes under
# <home>\cassettes) and with SLACK OFF (GHOST_DEMO_NO_SLACK=1: no Slack token is read, the bot is not started, nothing is posted, so no
# recorded message ever lands in the demo channel; the model calls do not depend on Slack). Then it plays ONE chronology through the
# real pipeline, the learning continuation, with the human working through Cliff's REAL Slack handlers over a fake Slack transport
# (the same code the live demo runs, so the presenter's identical typing replays from the cache):
#   MedTech Advances: Event N -> Message 1 -> Message 2 (strategies and evals) -> the human presses Select on Ghost's second choice,
#   edits the email in the Edit modal and presses Send (dry run: the send-time re-evaluation and the labeler are recorded) ->
#   Message 3 -> Edit interpretation (the correction) -> the knowledge forms (asserted: stamped at or before MedTech's Event N)
#   -> the same hidden handoff the second Play uses carries it into EcoLite Innovations (asserted: each entry keeps its own replay
#   time and predates EcoLite's Event N) -> that episode retrieves AND uses it (asserted from knowledge_attribution) -> the same
#   Slack path again.
# Everything the human typed is written to <home>\RUNSHEET.txt (D:\ghost-demo\RUNSHEET.txt), the presenter's hidden run sheet.
# Then it resets the whole chronology to Event N-1. Unless -SkipVerify, it repeats the chronology on the SAME path with
# GHOST_LLM_CACHE_STRICT=1 (a cache miss is an error, no network): if that passes, the rehearsal is fully recorded and costs nothing.
#
# Spend guard: real calls run one at a time and, before each, the OpenRouter key's usage (GET https://openrouter.ai/api/v1/key, data.usage)
# plus a local count of what was just spent must leave room under $7.50 (the hard cap is $8); otherwise the call is refused. Override with GHOST_LLM_SPEND_CAP_USD only to lower it.
# A run that stops half way can simply be run again: it starts from a reset chronology and the stored calls are replayed, not repeated.
# A human who later deviates from the recorded path triggers one real call per new request, recorded once.
param([switch]$SkipVerify)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')

function Step([string]$Text) { Write-Host ""; Write-Host "[record] $Text" }
function Show-Cache {
    $f = Join-Path (Get-DemoHome) 'cassettes\cache-stats.json'
    if (Test-Path $f) {
        $s = Get-Content $f -Raw | ConvertFrom-Json
        Write-Host ("cache: {0} recorded, {1} replayed, spend cap hit: {2}" -f $s.recorded, $s.hits, $s.spend_blocked)
        return $s
    }
    Write-Host 'cache: no calls yet'
    return $null
}

# Runs the whole record chronology once, with Slack off. The environment is restored whatever happens.
function Invoke-RecordPass([string]$Exe, [switch]$Strict) {
    $env:GHOST_DEMO_NO_SLACK = '1'
    if ($Strict) { $env:GHOST_LLM_CACHE_STRICT = '1' } else { Remove-Item Env:\GHOST_LLM_CACHE_STRICT -ErrorAction SilentlyContinue }
    try {
        if ((Invoke-Ghostctl $Exe @('codespace', 'up') 'record') -ne 0) { return 'start' }
        $code = Invoke-Ghostctl $Exe @('codespace', 'record') 'record'
        [void](Invoke-Ghostctl $Exe @('codespace', 'down') 'record')
        if ($code -ne 0) { return 'record' }
        return ''
    } finally {
        Remove-Item Env:\GHOST_DEMO_NO_SLACK -ErrorAction SilentlyContinue
        Remove-Item Env:\GHOST_LLM_CACHE_STRICT -ErrorAction SilentlyContinue
    }
}

$exe = Build-Ghostctl
Initialize-DemoHome

Step 'check: both cases are frozen'
$status = Get-DemoStatus $exe
if (-not $status -or @($status.cases | Where-Object { $_.seeded }).Count -lt 2) {
    throw 'both cases must be frozen first: run scripts\demo\setup-local.ps1'
}

Step 'record the chronology with Slack off (costs real model calls, once; nothing is posted to Slack)'
$before = Show-Cache
$failed = Invoke-RecordPass $exe
if ($failed -eq 'start') { throw 'start failed; see logs\record.log' }
if ($failed -eq 'record') {
    Show-Cache | Out-Null
    throw 'the record run stopped; see logs\record.log (calls already stored are kept and are not repeated: run this script again to resume)'
}
$after = Show-Cache

if ($SkipVerify) { Write-Host "`nrecorded. Verification skipped."; exit 0 }

Step 'verify: play the same chronology again with a strict cache (a miss is an error, nothing is called), Slack still off'
$failed = Invoke-RecordPass $exe -Strict
if ($failed -eq 'start') { throw 'start (strict) failed; see logs\record.log' }
if ($failed -eq 'record') {
    throw 'the strict replay hit a request with no recording: the chronology is not deterministic yet. See logs\record.log and logs\worker.log (a key that varies between runs).'
}
Show-Cache | Out-Null
Write-Host "`nrecorded and verified: a rehearsal now replays stored answers and costs nothing."
