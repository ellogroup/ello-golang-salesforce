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
| Medium | 0 | 0 | 0 | 0 |
| Low | 0 | 0 | 1 | 0 |

---

## TD-001 — zap stays a dependency via the public API; dropping it needs a v2

**Status:** Resolved (2026-10-02, v2.0.0, PR #4)
**Severity:** Low
**Category:** Dependencies
**Created:** 2026-10-02
**Created by:** AI-assisted — reviewed by Symeon Manis
**Owner:** Backend team
**Linked ticket:** None

**What is the debt?**
`TokenCache` now uses `ello-golang-cache/v2`'s `cachefunc` and logs through
slog. That removed the zap-only `ello-golang-cache` v1, the hand-written
zap→slog bridge, and v1's refresh bug (no retry for about an hour after a
failed refresh, ello-internal-app TD-006).

What remains needs breaking changes, which v1 can't take:

- **zap stays a dependency.** `NewTokenCacheWithLogger(p, *zap.Logger)` is
  public API, so zap (plus `go.uber.org/zap/exp` for its `zapslog` adapter)
  remains in every consumer's dependency graph.
- **`NewTokenFetcher` takes no `ctx`.** It reads Secrets Manager with
  `context.Background()`, so that call isn't traced or cancellable.
- **Exported names aren't Go-style:** `HttpClient`, `Id`, `BaseUrl`, etc.

**Why does it exist?**
These changes were made in a minor release (v1.3.0), so existing
signatures had to stay.

**What is the risk if unaddressed?**
Low. Consumers that have moved to slog still pull in zap, the Secrets Manager
read at construction is invisible to tracing, and lint rules on naming need
exceptions.

**Proposed resolution:**
A `github.com/ellogroup/ello-golang-salesforce/v2` major version that:

1. Removes `NewTokenCacheWithLogger(*zap.Logger)` and the zap and zap/exp
   dependencies; `NewTokenCacheWithSlogLogger` (or options) becomes the only
   way to log.
2. Takes a `ctx` in `NewTokenFetcher` / `NewTokenCache*` for the Secrets
   Manager read and the initial token fetch.
3. Renames exported identifiers to Go style.

**Consumer impact** (checked 2026-10-02, default branches). Nothing breaks
until a consumer changes its import to `/v2`:

| Repo | Uses | Change needed |
|---|---|---|
| ello-internal-app `app/` | `NewTokenCacheWithLogger` (zap), `RequestHelper`, `Query`/`Post`/`Patch` | Import path; switch to the slog constructor (planned anyway) |
| ello-internal-app `test/` | `NewTokenCache`, `Query`/`Post`/`Delete` | Import path, `ctx` arg |
| redemption-dispatcher-service | `NewTokenCache`, `TokenCache`, `TokenGetter` | Import path, `ctx` arg |
| customer-app-graphql | `NewTokenFetcher`, `RequestHelper`, `Query` | Import path, `ctx` arg |
| ello-customer-authorizer-lambda-app `test/` | `NewTokenFetcher` | Import path, `ctx` arg |

Renames would add small edits wherever `TokenParams.HttpClient` or the
renamed types are referenced.

**Effort estimate:** S — hours (API changes, release, consumer bumps)
**Resolution target:** Backlog

**Resolution:**
Done in v2.0.0 (`github.com/ellogroup/ello-golang-salesforce/v2`), as
requested in review on PR #4:

- zap is removed. The token cache logs through the optional
  `TokenParams.Logger` (`*slog.Logger`), and zap and zap/exp are no longer
  dependencies.
- `NewTokenFetcher(ctx, p)` and `NewTokenCache(ctx, p)` read Secrets Manager
  and fetch the first token with the caller's context.
- Exported identifiers are Go style: `HTTPClient`, `PostResponse.ID`,
  `Attributes.URL`. JSON tags are unchanged.
- `cenkalti/backoff` moves v4 → v7, so `TokenParams.Backoff` is now a v7
  `BackOff`.

The README's "Migrating from v1" section lists each change. Consumers move
by switching their import to `/v2`; the per-consumer edits are in the table
above.

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
