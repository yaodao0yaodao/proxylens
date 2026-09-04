# Runtime events, timeouts, retries, and concurrency

This document describes the implemented runtime behavior. Times are upper
bounds for an individual operation unless stated otherwise.

## Triggers

| Event | Trigger | Work |
| --- | --- | --- |
| Startup regeneration | Every process start | Recalculate stored quality and regenerate stored profiles in the background. Existing Web UI and artifacts remain available while this runs. |
| Scheduler scan | Startup, then every minute | Check task, rule, process-list, and retention deadlines. |
| Complete detection | Per-task/global interval, minimum 15 minutes | Refresh subscription → detect exit IP and steady latency → calculate quality → generate all client profiles. |
| New task | Successful task creation | Start one complete detection asynchronously. |
| Start/manual run | Start or run button | Enable the task and run after its current run finishes. Repeated ordinary triggers are coalesced into one pending run. |
| Global settings saved | Successful save | Cancel each enabled task's current run and start it again with the new settings. Paused tasks stay paused. |
| Task settings saved | Successful save | Enable that task, cancel its current run, and start it again with the new settings. |
| Four-hour detection | User confirmation | Repeat complete detections for four hours, with one minute between completed runs. |
| Stop four-hour detection | Stop button | Let the current detection finish, then stop the campaign. Pause cancels it immediately. |
| Rule refresh | Every 24 hours or incomplete cache | Refresh maintained SRS rule files. |
| Download-process refresh | Every 24 hours or missing cache | Refresh the Carton DIRECT process list. |
| Measurement retention | Startup, then at most hourly | Keep raw measurements 48 hours, hourly summaries 90 days, cycle quality snapshots 48 hours, and daily quality summaries one year. |
| Database compaction | Startup, then every 24 hours | Enforce the configured database size ceiling and compact SQLite. |
| Web refresh | Every three seconds while no dialog is open | Fetch task state and overview concurrently. |

Generated client profiles also contain two client-side timers: automatic node
testing every 10 minutes while active (30-minute idle timeout), and remote rule
set refresh every 12 hours.

## Timeouts and retry policy

| Operation | Timeout | Retry/fallback |
| --- | --- | --- |
| Complete detection requested by Web/scheduler | 6 hours | No blind whole-run retry. The next scheduled run remains available. |
| Four-hour campaign wrapper | 4 hours 15 minutes | Repeats after each completed run until its four-hour deadline. |
| Subscription HTTP request | 45 seconds; 16 MiB body limit; redirect limit | Direct once, then once through the best eligible internal proxy. Existing nodes remain active on failure. |
| sing-box probe startup ports | 10 seconds | A local probe-process failure is retried once with that node isolated. |
| Cycle HTTP request | 5 seconds per request | Exit-IP discovery tries three independent endpoints; steady latency performs one warm-up plus three samples and needs at least two valid scored samples. |
| Direct latency baseline | Same 5-second request timeout | Failure or a baseline of at least 800 ms aborts the batch so a broken local route is not charged to nodes. |
| Node latency | 800 ms quality threshold | At least 800 ms is stored as unavailable and no latency value is recorded. |
| Exit-IP provider | At least 2 seconds per endpoint inside the cycle request limit | Three providers in fixed fallback order. |
| IP country/ASN lookup | 15 seconds | No automatic retry in the same cycle; a later detection tries again. Up to three independent lookups run concurrently. |
| Each maintained rule HTTP request | 30 seconds; 8 MiB limit | Direct pass, then only failed providers retry once through the best internal proxy. Failed campaigns retry no sooner than 15 minutes and retain valid cache. |
| Download-process list | 30 seconds; 1 MiB limit | Direct once, proxy fallback once; retry no sooner than 15 minutes and retain cache. |
| HTTP server | headers 10 seconds; read 30 seconds; write 5 minutes; idle 2 minutes | No server-level request replay. |
| Graceful shutdown | 15 seconds | Outstanding work receives cancellation through the process context. |

## Concurrency boundaries

- Different tasks may fetch subscriptions concurrently.
- Only one sing-box probe or internal-proxy operation runs at a time; this
  prevents local port collisions.
- Node network tests inside one probe process use four workers.
- Exit-country and server-ASN lookups use three workers, but database updates
  remain sequential and deterministic.
- Rule maintenance runs independently from task scheduling. Direct downloads
  do not require the probe lock; proxy fallbacks do.
- Quality calculation must wait for the complete probe result, and profile
  generation must wait for quality calculation. These stages are intentionally
  not detached because doing so could publish mixed-generation data.
- The four client variants are generated sequentially to limit peak CPU and
  memory use on routers.
