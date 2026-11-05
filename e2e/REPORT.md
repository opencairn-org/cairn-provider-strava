# e2e strava — PASS

- started: 2026-11-05T21:53:28Z, total 55.852s
- core: v0.7.0-2-g4a85ac8 (fix/conformance-active-subject@4a85ac8) at http://localhost:37577, db cairn_e2e_20261105_215326, webhook-verify fix: true, conformance --active fix: true
- spec: ../cairn-provider-strava/e2e/strava.yaml
- worker: strava-fetcher (strava-fetcher-strava) release v0.7.0 from ../cairn-provider-strava; mock http://127.0.0.1:40385
- presence: package cairns.internal.strava-importer, parser 1.1, release v0.7.0, webhooks true
- account: 01a1adb3-e28b-751b-862e-9fd7c46106d3 (provider id 4242, pool strava-fetcher)

| step | status | time | detail |
|---|---|---|---|
| build worker+mock | PASS | 162ms | binaries in ../cairn-provider-sdk-go/e2e/out/strava |
| core release | PASS | 2ms | core v0.7.0-2-g4a85ac8 (info key v0.7.0-2-g4a85ac8), worker stamped v0.7.0 |
| admin login | PASS | 109ms | session cookie for admin@e2e.local |
| mock up | PASS | 503ms | mock at http://127.0.0.1:40385 |
| worker presence | PASS | 514ms | pool strava-fetcher release v0.7.0, provider "Strava" auth oauth2 |
| connect account | PASS | 55ms | account 01a1adb3-e28b-751b-862e-9fd7c46106d3 via oauth2 (provider account 4242) |
| reconcile on connect | PASS | 4.53s | last_sync_at 2026-11-05T21:53:34Z, 0 activities imported |
| gear on connect | PASS | 58ms | 1 pending refs decided as new gear: Canyon Grail |
| discover | PASS | 22ms | total 3, already present 0, new 3 |
| full sync | PASS | 5.6s | imported 3 (provider total 3), queue map[done:3 failed:0 in_progress:0 pending:0 skipped:0], 3 fetch_source jobs |
| activity data | PASS | 1.107s | 3 activities of type ACTIVITY_TYPE_RIDE, sources stamped strava-fetcher/1.1; merged stream channels [grade latitude longitude speed altitude distance heart_rate power temperature cadence] |
| reparse | PASS | 3.546s | source 01a1adb4-0883-7e3d-963d-88dd491674c4 re-parsed at 2026-11-05T21:53:44.252001Z, parser 1.1 |
| rate limit 429 | PASS | 17.183s | activity 1000004 imported after one 429 (retry-after 2s) |
| reconcile | PASS | 10.132s | activity 1000005 found by reconcile and imported |
| webhook event | PASS | 4.607s | event for 1000006 forwarded, fetched and imported |
| webhook verify | PASS | 3ms | challenge echoed (CAIRN_WEBHOOKS subjects [cairn.webhooks.*.event]) |
| import metrics | SKIP | 0s | disabled in spec |
| token refresh | PASS | 0s | 1 refresh, 1 exchanges, 31 API calls, 0 unauthorized |
| worker-conformance | PASS | 2.72s | PASS  presence heartbeat<br>PASS  consumer for cairn.jobs.fetch_source.strava.strava-fetcher<br>PASS  consumer for cairn.jobs.reconcile.strava.strava-fetcher<br>PASS  consumer for cairn.jobs.parse_blob.strava.strava-fetcher<br>PASS  consumer for cairn.jobs.import_gear.strava.strava-fetcher<br>PASS  discover request/reply<br>PASS  reconcile round-trip<br><br>worker is contract-compliant |
