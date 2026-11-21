# Review

[Documentation index](wiki/README.md) · [简体中文](REVIEW.zh_CN.md)

This is a historical review of the SQLite runtime migration. For current behavior and installation, see the [documentation index](wiki/README.md).

## Scope

This review checks `dev-runtime` against `dev` with the following standards:

- Do not accidentally remove or override teammate payment capabilities already merged into `dev`.
- Do not submit local test artifacts or dirty files.
- Confirm Redis -> SQLite replacement is functionally complete for the intended single-instance target.
- Judge code quality, elegance, robustness, and maintainability.
- Check for real bugs, mojibake, and redundant code.
- Compare current quality with the previous `dev` implementation.

These standards are also the default development constraints for future work:

- no invalid tests
- no mirror tests
- prefer behavior-based tests over implementation-shaped tests
- explain intentional semantic changes in review notes
- verify dirty local artifacts are excluded before commit
- think about teammate-regression risk before coding, not only after coding

## Files That Must Not Be Committed

These local SQLite artifacts must never be committed:

- `src/epusdt.db`
- `src/epusdt.db-shm`
- `src/epusdt.db-wal`

They are now excluded by `.gitignore`.

## Teammate Capabilities Still Preserved

The following teammate payment capabilities are still present:

- multi-currency order model: `src/model/mdb/orders_mdb.go`
- wallet address model: `src/model/mdb/wallet_address_mdb.go`
- native `TRX` transfer detection: `src/model/service/task_service.go`
- `TRC20 USDT` detection: `src/model/service/task_service.go`
- dynamic exchange-rate path: `src/model/service/order_service.go`, `src/config/config.go`
- `TRON_GRID_API_KEY` support: `src/config/config.go`

Conclusion: teammate core payment capabilities were not accidentally removed.

## Intentional Architecture Changes

These are intentional refactors, not accidental overrides:

- Redis runtime removed: `src/model/dao/rdb.go`
- asynq handlers removed: `src/mq/handle/callback_queue.go`, `src/mq/handle/order_expiration_queue.go`
- SQLite runtime lock introduced: `src/model/mdb/transaction_lock_mdb.go`
- SQLite scheduler introduced: `src/mq/queue.go`, `src/mq/worker.go`
- reservation moved from Redis key to SQLite unique constraint: `src/model/data/order_data.go`

Conclusion: this branch keeps payment capability but changes the runtime implementation.

## Previously Regressed Parts That Are Now Restored

The following regressions have been restored in the current working tree:

- rich Telegram payment notification template: `src/model/service/task_service.go`
- key chain-scan observability logs: `src/model/service/task_service.go`
- Chinese checkout error message: `src/model/service/pay_service.go`
- more reliable Telegram add-wallet flow: `src/telegram/handle.go`

## Functional Completion

For the single-instance target, the Redis -> SQLite replacement is complete across:

- order creation
- amount reservation
- chain scan matching
- payment success processing
- order expiration
- callback scheduling
- Telegram notification

Conclusion: feature work is complete for the intended single-instance model.

## Mojibake Check

No real mojibake was found in the source files checked during review.

Files spot-checked:

- `src/telegram/handle.go`
- `src/model/service/task_service.go`
- `src/model/service/pay_service.go`

Note: some terminals may render UTF-8 Chinese poorly, but the source content itself is fine.

## Redundant Code Check

No obvious redundant business code was found.

Recent helper additions are justified:

- `PendingCallbackOrder`
- `expirableOrder`
- `configureSQLite`

Conclusion: no clear "fix by duplication" smell was found.

## Bug Assessment

No confirmed blocking business bug is currently visible in the reviewed code path.

Important risk that still needs attention:

- SQLite main DB can still hit `SQLITE_BUSY` under concurrent polling and writes.

This risk is now being mitigated by:

- `src/model/dao/sqlite_config.go`
- `src/model/dao/mdb_sqlite.go`
- `src/model/dao/runtime_sqlite.go`
- lightweight busy retry in `src/mq/worker.go`

Conclusion: no confirmed blocking functional bug, but SQLite concurrency remains a real operational boundary that should still be watched in runtime.

## Robustness Assessment

Strengths:

- conditional state transitions prevent reviving expired orders
- reservation is now a persistent unique constraint instead of a temporary Redis key
- callback state is durable across process restarts
- behavior tests exist for key paths:
  - `src/model/service/order_service_test.go`
  - `src/mq/worker_test.go`
- callback polling now has a small targeted retry for transient SQLite busy errors

Remaining limits:

- robust for single-instance deployment
- not a final design for multi-instance deployment
- SQLite contention should still be watched in real runtime

Conclusion: robust enough for single-instance use, not a direct multi-instance design.

## Elegance Assessment

Strengths:

- runtime responsibility is cleaner with SQLite reservation + scheduler
- logging responsibility is split more clearly:
  - `log_level`
  - `http_access_log`
  - `sql_debug`
- Telegram input flow is more practical for real clients
- SQLite tuning is extracted into one helper

Tradeoffs:

- callback semantics are no longer the same as the old immediate-asynq enqueue path
- current branch still has local uncommitted fixes that must be organized into a clean commit set

Conclusion: elegant for the chosen single-instance direction, but must be explained clearly when merging.

## Quality Compared With Previous Code

Overall judgment: quality did not go down; it improved in the single-instance direction.

Improvements:

- stronger state-machine constraints
- better log control
- more durable runtime reservation
- better Telegram interaction resilience
- added behavior tests
- added restart-oriented callback recovery coverage
- reduced scheduler fragility under transient SQLite busy errors

Changed semantics rather than degradation:

- merchant callback is now polled by SQLite scheduler instead of immediate asynq enqueue

Conclusion: quality is not lower, but runtime semantics changed and must be stated explicitly.

## Self Score

- functional completeness: 9.1/10
- engineering implementation: 8.9/10
- robustness: 8.7/10
- elegance: 9.0/10
- commit readiness: 8.8/10

Overall self score: 8.9/10

Main deductions:

- current uncommitted fixes still need to be organized into a clean commit
- SQLite single-file concurrency boundary still exists
- callback semantics differ from `dev-payment` and require clear communication

## Final Conclusion

This code can be submitted, but only if all of the following are done:

1. Do not commit local SQLite artifacts.
2. Include the current uncommitted fixes in a clean, intentional commit set.
3. Explain clearly in merge notes:
   - what teammate capabilities were preserved
   - what runtime behavior was intentionally refactored
   - what semantics changed and why

If those three conditions are met, this is not a hidden override of teammate code. It is a reviewed and documented refactor.
