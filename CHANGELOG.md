# Changelog

## [0.2.0](https://github.com/bbmumford/cfgload/compare/v0.1.0...v0.2.0) (2026-11-25)


### Features

* a load commits only a document every step accepted, a judge the caller supplies decides each verified header, and a watch retries a failed first load and stops whole ([e3a9c09](https://github.com/bbmumford/cfgload/commit/e3a9c099a7534362c421617f05cb555775c5c763))
* LoadBytes loads a signed document handed as bytes, judged like a loaded one ([2cc0909](https://github.com/bbmumford/cfgload/commit/2cc09098477b4fc03624bae195d0b20bdf992824))


### Bug Fixes

* a judged load never falls back to the environment ([5795887](https://github.com/bbmumford/cfgload/commit/5795887f23feddbbb8a22b2923835551be550402))
* each subscriber receives updates in the order they were stored ([a8bde0c](https://github.com/bbmumford/cfgload/commit/a8bde0c8d886fa034bbcb60af07aa588d50b6cb6))
* go.mod requires envreq v0.0.2, the release holding the envreq code this module builds against ([14dec4d](https://github.com/bbmumford/cfgload/commit/14dec4d6a3550f577415ce55e30b06f6d6c5c992))
* the fetcher keeps the token off the network in the clear and bounds the body ([c7b9bfc](https://github.com/bbmumford/cfgload/commit/c7b9bfc6ebafb091aa88ada629ea978e7f329d24))
