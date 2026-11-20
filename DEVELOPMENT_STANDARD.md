# Development Standard

## Purpose

This document defines the default development goals, review criteria, and testing constraints for changes in this repository.

Before starting any implementation, use this document as a checklist.

## Development Goals

Every change should be judged against all of the following goals:

1. Functional completeness
   The target feature must actually work end to end, not just compile.

2. No accidental teammate regression
   Do not accidentally remove or override teammate capabilities that already exist on the base branch.

3. Robustness
   The code should handle normal failure cases, retries, state conflicts, and restart scenarios as reasonably as possible.

4. Elegance
   Prefer clear responsibilities, minimal coupling, and a small number of well-named moving parts.

5. Maintainability
   Avoid hidden behavior, confusing configuration, or implementation details that make future debugging harder.

6. No dirty output
   Never commit local runtime artifacts, local databases, caches, logs, or other development byproducts.

7. No mojibake
   Source files, messages, and docs should not contain broken encoding text.

## Multi-Dimensional Self Review

Each meaningful change should be evaluated across these dimensions:

- functional completeness
- engineering implementation quality
- robustness
- elegance
- maintainability
- regression risk
- commit readiness
- test coverage quality

Scoring should be honest. High scores require both working behavior and clean delivery quality.

## Code Quality Rules

### Redundancy

Avoid redundant code unless duplication is clearly cheaper and safer than abstraction.

The following are considered bad redundancy:

- duplicate business logic with only tiny naming differences
- duplicate retry or state logic in multiple places
- helper functions that exist only to wrap one line without improving clarity

The following are acceptable:

- small data shapes for read optimization
- isolated helpers that remove repeated infrastructure setup
- compatibility shims with clear removal intent

### Elegance

Code is considered elegant when:

- responsibilities are split cleanly
- names explain intent
- state transitions are explicit
- configuration is understandable
- behavior is observable through useful logs
- failure handling does not hide important problems

### Robustness

Code is considered robust when:

- state changes are guarded by conditions where needed
- retries are controlled, not infinite
- restart recovery is possible where it matters
- local concurrency does not easily corrupt behavior
- failure in one side task does not silently break the main flow

## Bug Standard

Ask these questions before treating a change as finished:

- Is there any blocking functional bug?
- Is there any likely edge-case bug?
- Is there any operational bug under realistic local concurrency?
- Is there any user-visible regression?

If a bug is known, it must be called out explicitly in review notes.

## Testing Rules

### Required Principle

Tests must validate behavior, not re-implement the code under test.

### Forbidden Test Types

Do not write:

- invalid tests
- mirror tests
- tests that only repeat implementation branches line by line
- tests that only verify mocks interacted in the same order as the code was written
- tests that lock onto incidental internals instead of real behavior

### Preferred Test Types

Prefer:

- end-to-end behavior tests
- state transition tests
- concurrency conflict tests
- retry and recovery tests
- expiration and timing behavior tests
- persistence and restart-oriented tests where relevant

### Test Coverage Expectations

Coverage quality matters more than raw coverage count.

For core flows, tests should cover:

- normal success path
- important failure path
- state conflict or idempotency path
- retry or delayed processing path where relevant

## Review Checklist

Before considering a change ready, confirm:

- functionality is complete
- teammate capability was not accidentally lost
- no local artifact will be committed
- no mojibake exists
- no obvious redundant code exists
- no known blocking bug remains
- robustness is acceptable for the target deployment model
- tests are behavior-based and not mirror tests
- review notes clearly explain intentional semantic changes
- if API handlers or swag annotations changed, `make swagger` was run in `src/` (swag v1.16.6) and the regenerated `src/docs` is committed (CI fails otherwise; the frontend pulls `src/docs/swagger.json` from GitHub)

## Current Project Direction

For this repository specifically, current review should assume:

- single-instance deployment is the primary target unless stated otherwise
- runtime semantics may intentionally differ from older Redis and asynq behavior when the new SQLite design is deliberate
- any such semantic difference must be explained in merge or review notes
