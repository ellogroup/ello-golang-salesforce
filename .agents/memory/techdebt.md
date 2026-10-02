# Tech Debt Register

A structured log of known technical debt in this library. Entries are
created at the point debt is identified — not retrospectively.

Honest entries are more useful than polished ones. The reason a piece of debt
exists is as important as the debt itself.

---

## Summary

| Severity | Open | In Progress | Resolved | Accepted |
|---|---|---|---|---|
| Critical | 0 | 0 | 0 | 0 |
| High | 0 | 0 | 0 | 0 |
| Medium | 1 | 0 | 0 | 0 |
| Low | 0 | 0 | 0 | 0 |

---

## TD-001 — Token cache logging depends on zap; slog-only needs a v2

**Status:** Open
**Severity:** Medium
**Category:** Dependencies
**Created:** 2026-10-02
**Created by:** AI-assisted — reviewed by Symeon Manis
**Owner:** Backend team
**Linked ticket:** None

**What is the debt?**
`TokenCache` is built on `ello-golang-cache` v1's `KeylessRecordCache`, which
only accepts a `*zap.Logger`. This library doesn't log anything itself; the
logger exists only to pass to that cache.

- **zap stays a dependency.** `NewTokenCacheWithLogger(p, *zap.Logger)` is
  public API.
- **slog goes through a bridge.** `NewTokenCacheWithSlogLogger` can only
  wrap slog in an internal zap core (`salesforce/zapslog.go`). Those log
  entries carry no context, so no trace IDs or context fields.
- **It inherits a refresh bug from cache v1.** `refreshCache` marks a refresh
  as done even when it failed, so after a token outage longer than the
  fetcher's back-off (about 15 minutes), nothing retries for 58 minutes.
  Once the old token is older than 59 minutes, every `Get` fails with
  `"value not in cache and on demand fetcher is not initalised"`. A failed
  first fetch at construction is swallowed the same way, so callers can't
  retry it. See ello-internal-app TD-006.

**Why does it exist?**
The library predates the move to `log/slog`, and `ello-golang-cache` v1
only supports zap. Adding slog in v1.3.0 had to be additive, so the zap API
stays and the slog constructor bridges to it.

`ello-golang-cache` v2 (`cachefunc`) is not a drop-in replacement: it has no
logger and no background refresh. Using it would make one request roughly
every hour wait for token generation (JWT signing plus 2 HTTP calls, with
retries).

**What is the risk if unaddressed?**
- Consumers moving to slog keep zap in their dependency graph.
- Token-cache log lines can't be correlated with traces.
- The refresh bug can leave every consumer unable to call Salesforce for up
  to about an hour after an auth outage recovers. Today that's
  ello-internal-app and redemption-dispatcher-service.

**Proposed resolution:**
Release a `github.com/ellogroup/ello-golang-salesforce/v2` major version that:

1. **Owns the token refresh** in place of `ello-golang-cache` v1 (roughly
   60–80 lines plus tests):
   - fetch synchronously at construction and return an error if it fails;
   - refresh before expiry;
   - retry soon after a failed refresh;
   - keep serving the current token while it is still valid;
   - add a `Stop()`, since v1's cron goroutine can't be stopped.
2. **Logs with `*slog.Logger` only**, using its context-aware methods where
   a context exists. Drops `NewTokenCacheWithLogger(*zap.Logger)`,
   `zapslog.go`, and the zap and `ello-golang-cache` dependencies.
3. **Optionally takes the other breaking changes at the same time:**
   - threading `ctx` through `NewTokenFetcher` (Secrets Manager read) and the
     token requests (`obtainToken`/`introspect`, which build requests
     without a context);
   - Go-style naming (`HttpClient` → `HTTPClient`, `Id` → `ID`,
     `BaseUrl` → `BaseURL`).

**Consumer impact** (checked 2026-10-02, default branches). Nothing breaks
until a consumer changes its import to `/v2`:

| Repo | Uses | Change needed |
|---|---|---|
| ello-internal-app `app/` | `NewTokenCacheWithLogger` (zap), `RequestHelper`, `Query`/`Post`/`Patch` | Import path, and switch to the slog constructor |
| ello-internal-app `test/` | `NewTokenCache`, `Query`/`Post`/`Delete` | Import path |
| redemption-dispatcher-service | `NewTokenCache`, `TokenCache`, `TokenGetter` | Import path |
| customer-app-graphql | `NewTokenFetcher`, `RequestHelper`, `Query` | Import path, plus a `ctx` arg if item 3 is done |
| ello-customer-authorizer-lambda-app `test/` | `NewTokenFetcher` | Import path, plus a `ctx` arg if item 3 is done |

If the v2 is postponed, fix the refresh bug in `ello-golang-cache` v1 on its
own (only advance `lastUpdated` after a successful refresh).

**Effort estimate:** M — days (refresher, tests, release, consumer bumps)
**Resolution target:** Backlog

---

## Template

### TD-[NNN] — [Short descriptive title]

**Status:** [Open | In Progress | Resolved | Accepted]
**Severity:** [Critical | High | Medium | Low]
**Category:** [Security | Performance | Architecture | Code Quality | Dependencies | Operational]
**Created:** [DATE]
**Created by:** [Developer name or "AI-assisted — reviewed by [name]"]
**Owner:** [Team or person responsible for resolution]
**Linked ticket:** [Ticket reference or None]

**What is the debt?**
[Clear, specific description of the problem. Not "this could be better" but
what exactly is wrong or missing.]

**Why does it exist?**
[Honest reason — time pressure, unclear requirements, legacy decision, known
shortcut taken deliberately, etc. This is not a blame record — it is context.]

**What is the risk if unaddressed?**
[Concrete impact. Not "it might cause problems" but what specifically breaks,
degrades, or becomes harder as a result of leaving this unresolved.]

**Proposed resolution:**
[What needs to be done to fix it. Does not need to be a full solution — a
direction is sufficient.]

**Effort estimate:** [S — hours | M — days | L — week+ | XL — significant]
**Resolution target:** [Current sprint | This quarter | Next quarter | Backlog | Accepted risk]

---
