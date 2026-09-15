# Changelog

All notable changes to this project are documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `RemoveRule` removes rules at run time without blocking `Match`.
- `MatchJSON` and `AppendMatchesJSON` match events given as JSON objects. They decode only the values that rules refer to, and nested objects and arrays become paths such as `alert.labels.team`.
- `ErrInvalidEvent` for events that are not valid JSON objects.

## [2.0.0] - 2026-09-15

The matching engine has been rewritten for correctness, speed and concurrent use. See [Migrating from v1](README.md#migrating-from-v1) for the API changes.

### Added
- `Match` is lock-free and can run concurrently with `AddRule`. All methods of `HyperMatch` are safe for concurrent use, and the zero value is ready to use.
- `AppendMatches` for allocation-free matching.
- `RuleCount` and `ErrInvalidRule`.
- Validation errors describe the location of the problem, for example `condition "name": anyOf[1]: [wildcard] must not contain two consecutive wildcards`.
- Runnable examples, benchmarks, and differential and fuzz tests against a reference implementation of the documented semantics.

### Changed
- The module path is `github.com/SchwarzDigits/hypermatch/v2`.
- `HyperMatch` is generic over the rule identifier type: `New[T]()` replaces `NewHyperMatch()` and `RuleIdentifier`.
- Rules are compiled into a trie of shared, normalized conditions with hash and automaton indexes. Matching is several times to several orders of magnitude faster, and rules use less memory. See [Performance](README.md#performance).
- `Match` returns the identifiers in the order in which the rules were first added, without duplicates, and `nil` if no rule matches.
- `AddRule` validates rules and rejects invalid ones with an error instead of compiling them.
- `ValidateRule` rejects empty condition sets.
- Decoding JSON rejects unknown pattern types and patterns or conditions with more than one key. Decoded condition sets are sorted by path, and encoding merges several conditions on the same path into an `allOf` pattern.
- Go 1.24 or later is required.

### Fixed
- Wildcard, prefix and suffix patterns overwrote transitions of other rules, so that rules missed matching events or matched wrong ones.
- `anythingBut` matched string arrays that contained an excluded value.
- `anyOf` conditions could make other rules that share a condition match wrong events.
- Values containing bytes that are not valid UTF-8 were considered equal to each other.
- `anythingBut` with a value instead of sub-patterns, and identifiers that are not comparable, caused panics. Unknown pattern types and empty paths were silently ignored, so that rules matched more events than intended.
- Calling `AddRule` and `Match` concurrently crashed the program.
- `AddRule` and `Match` reordered the slices passed to them.

### Removed
- The dependency on `gotest.tools`.

[Unreleased]: https://github.com/SchwarzDigits/hypermatch/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/SchwarzDigits/hypermatch/releases/tag/v2.0.0
