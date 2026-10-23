# Multi-task Scheduling and Performance

## Scheduling policy

`PUT /api/settings` accepts three independent settings:

```json
{
  "task_concurrency_enabled": true,
  "task_concurrency_limit": 5,
  "task_round_robin": true
}
```

The `workers` setting (default 3) remains the per-task worker pool size. Five
admitted tasks can therefore have up to 15 workers plus their planners. This is
not an API request-rate limit; configure the LLM profile's rate limits separately.

Across all admitted tasks, execution also passes through a process-wide worker
budget (default 15, configurable with `ZEROX_MAX_CONCURRENT_WORKERS`). Raising the
task-concurrency limit therefore does not multiply worker DB/LLM clients without
bound. Planners remain bounded by the task-concurrency admission itself.

A scheduling round consists of planning followed by a finite snapshot of at most
`workers` open intents, ordered by the existing priority/FIFO policy. Seeded work
can execute before the first planning pass, as before. Planners can supervise
running workers; any additional intents wait for the next batch. A round ends
only after every admitted execution and its final database writes have drained.
No agent, tool, transcript, or evidence is truncated to force a scheduling yield.

The completed task joins the persistent FIFO queue with `paused=false`. The next
ready task is admitted immediately, without waiting for the periodic scheduler.
Queued tasks survive restart; resumed tasks keep their graph and transcripts.
Manual pause, deletion barriers, unavailable models, and terminal states retain
their existing control semantics. Disabling round-robin restores continuous
execution. Changing settings while agents are running takes effect at safe
boundaries, rather than killing those agents.

This is **work-conserving FIFO**, not a global round barrier: all earlier queued
tasks get a slot before the yielded task is readmitted. Waiting for the very last
task to *finish* before starting any second round would leave slots empty at the
end of each cycle and cannot also guarantee five occupied slots. Concurrency is
an upper bound: manual pauses, model outages, completed tasks, and lack of work
can leave fewer than five runnable tasks.

An empty round parks until the existing planner heartbeat or a new graph event;
it does not spin more empty LLM calls just to keep slots busy. Existing absolute
task deadlines are unchanged: the configured wall-clock timeout includes queue
waiting, just as it includes manual pauses. Use an appropriate timeout for large
queues. The cap applies to autonomous task execution, not independent interactive
chat, custom triggered agents, or the existing deadline-finalization mechanism.

## Stable finding numbers

- Original finding: `0X-V-00000042` (standalone finding ID, not exploration node ID).
- Consolidated defect: `0X-C-00000009` (case ID).
- Legacy graph-only finding: `0X-N-00000200` (explicitly separate namespace).

Numbers derive from immutable persisted primary keys, not names, severity,
timestamps, row positions, or LLM output. Existing records need no migration.
They are unique within a deployment; database backup/restore preserves them.
Repeated observations can be grouped into one numbered defect without deleting
their original numbered records. Search accepts original/case numbers.

Task/global Markdown, per-finding ZIP filenames and contents, CSV, JSON DTOs,
finding detail/copy actions, and finding tools expose the number. File truncation
retains the leading number and valid UTF-8 even for long non-ASCII titles.

## Performance changes

1. Worker lifecycle gates run before model-profile resolution. Queued, paused,
   terminal and round-blocked workers do not repeatedly query profiles.
2. Worker wake-ups use task-local broadcast signals. A five-second fallback
   protects database-only writers; graph changes and resumes wake immediately.
3. Continuous-mode frontier claiming uses one `UPDATE ... RETURNING` with
   `FOR UPDATE SKIP LOCKED`, instead of loading 20 nodes and racing per-node CAS.
   Round-robin retains the bounded snapshot and explicit per-intent CAS.
4. Task report asset counts use one scoped aggregate instead of materializing
   up to 100,000 assets for each of six global asset types. Reports no longer
   include other tasks' asset counts.
5. Planner debounce respects shutdown cancellation and stops its timer.
6. Batch pause cancels every selected task before one bulk `UPDATE`, then runs a
   single post-batch admission pass. This prevents the old loop from pausing one
   task and immediately admitting a queued replacement during the same request.
7. Cold-graph maintenance is throttled to 30 seconds, uses a topology-only query
   without JSONB payloads, allows at most three concurrent maintenance scans and
   two background compactions, and inherits the task context so pause/delete
   cancels compression immediately.
8. PostgreSQL keeps at most 20 open and 6 idle connections; idle connections are
   released after one minute. This bounds backend processes after a multi-task
   burst without starving normal query concurrency.

No model, prompts, context windows, maximum turns, evidence retention, tool
permissions, or discovery logic are reduced by these optimizations.

## Reproducible verification

Use a disposable PostgreSQL database; tests mutate its schema and data. Do not
point tests at a production database. Set `ZEROX_PG_DSN` to the disposable database.

```bash
go test ./report
go test ./agent -run TestEnvThinkingAndReasoningEffortReachChatCompletions
go test ./server -run 'TestTaskRound|TestRound|TestQueuedWorkers' -count=1
go test -race ./server ./db -run 'TestTaskRound|TestRound|TestQueuedWorkers|TestClaimNext' -count=1
go test ./db -run '^$' -bench '^BenchmarkClaimIntent$' -benchtime=500x -benchmem
cd web && npx tsc --noEmit && npm run build:static
```

`TestRoundRobinRealEngineLocalTargetThirtyTasks` runs 30 real Engine tasks, three
workers per task, six intents each, against an in-process loopback HTTP fixture.
It exercises real tool calls, finding persistence, scheduling, report rendering,
and shutdown. A deterministic model fixture keeps the test repeatable and free
of external API costs. It checks 180 successful probes, 180 unique finding numbers,
all 180 settled intents, and at most five simultaneously calling tasks. This is
an execution/correctness test, not a claim about real-world vulnerability yield.

`TestLiveLLMLoopbackFindingAndExport` additionally verifies an actual configured
model against two owned loopback records, then persists and exports its finding.
It is skipped unless `ZEROX_LIVE_LLM_TEST=1` is explicitly set. The model sees only
two tools: a fixed loopback record reader and the finding submission tool. There
is no shell, arbitrary-URL fetcher, or external target. It rejects reports before
both responses have actually been delivered to the model. This test spends a
small amount of API credit; supply credentials via your local environment and
use a disposable `ZEROX_PG_DSN`, never a production database.

```bash
ZEROX_LIVE_LLM_TEST=1 go test ./server -run '^TestLiveLLMLoopbackFindingAndExport$' -v -count=1
```

`BenchmarkClaimIntent` compares the old frontier/CAS algorithm with the new
atomic claim on the same database. Compare allocation counts and query volume
as well as wall time: PostgreSQL fsync, container storage, and other workloads can
dominate timing on development machines. Do not infer a CPU or throughput gain
from a single wall-time sample.

One local verification run (PostgreSQL 16 container, Linux amd64, 500 operations
per sample, three samples per implementation) produced these results:

| Metric | Frontier + CAS | Atomic SKIP LOCKED |
| --- | ---: | ---: |
| Allocated bytes per claim | 13,209-13,220 | 2,237 |
| Allocations per claim | 317 | 51 |
| SQL round trips per successful claim | at least 2 | 1 |
| Wall time per claim | 3.67-4.37 ms | 3.72-4.46 ms |

Allocated bytes fall by about 83% and allocations by about 84%; the SQL round
trip count is halved. Wall-time ranges overlap, so these measurements do **not**
establish a latency/throughput improvement or a quantified physical disk-IO gain.

Live target testing must use assets you own or have authorization to assess.
No active scan of `example.org` is required or performed by these tests.

An unfiltered local regression run also reproduces three existing failures from
baseline commit `9a63967`, verified against a separate disposable database:
`TestGraphOverviewExpandsAssociatedCompanyScope`, `TestCaptureApprovalLifecycle`,
and `TestInterceptApprovalFilters`. They are not silently disabled in the code.
Excluding exactly those baseline failures, the cross-package regression suite
passes. The new scheduling, numbering, provider-wire, local-target and race tests
are run without skipping their assertions.
