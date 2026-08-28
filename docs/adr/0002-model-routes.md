# ADR 0002: Model identity is a route, `(provider, id, plane, region)`

**Status:** accepted, 2026-08-26

## Context

A catalog row was keyed by `(provider, id)` (`catalogKey`). Region, invoke
strategy, and endpoint were optional *payload* on that row rather than part of
its identity.

Bedrock does not fit that shape. The same model can be served:

- on two different planes (classic Converse, and the mantle REST plane), with
  different wire dialects, different pricing, and separately granted access;
- in several regions, where entitlement is granted per region;
- behind several inference profiles (`us.`, `global.`) that are distinct
  invokable ids.

With one row per model, the extra dimensions had to be discarded at discovery
and then guessed back at invoke time. Discovery collapsed the matrix at four
sites, and resolution guessed the route from how the id was spelled, using
profile-prefix stripping plus three hand-written vetoes bolted on after each
incident.

The guess was wrong in production three times (2026-08-20 twice, 2026-08-21),
every time routing a mantle-only id onto classic Converse. The live symptom:

```
ValidationException: Invocation of model ID xai.grok-4.6 with on-demand
throughput isn't supported. Retry your request with the ID or ARN of an
inference profile that contains this model.
```

Nothing warned beforehand, because Bedrock readiness is a control-plane
`ListFoundationModels` probe, not an invocation: `ai status` and `vault status`
both reported the model `[reachable]` right up until a real generate.

Measured against the live discovery caches on 2026-08-26, across 120 distinct
discovered models:

| | |
|---|---|
| ids present in more than one cell | 50 |
| ids on BOTH planes (mantle row discarded) | 26 |
| mantle-only ids (unreachable if misrouted) | 30 |
| mantle-only in exactly one region | 6 |
| catalog rows carrying any routing at all | 3 of 80 global, 11 of 78 vault |

That last row is the point: ~95% of rows depended on the same fallback that
broke grok 4.6. It was not an edge case, it was the default state.

## Decision

**A catalog row's identity is its route: `(provider, id, plane, region)`.**

- `Plane` (`classic` / `mantle`) selects the CLIENT; `InvokeStrategy` selects
  the ENVELOPE within it. Non-Bedrock providers leave both plane and region
  empty, so their key is byte-identical to the old form.
- Discovery emits one row per `(plane, region)` and walks both planes across
  the documented US regions. Nothing is collapsed.
- The config names the route explicitly (`ai.generation_plane` /
  `ai.generation_region`, the embedding pair, and the nested
  `ai.rerank.plane` / `ai.rerank.region`), written as one unit with the model.
- **Invocation refuses rather than guesses.** A slot whose model has several
  routes and names none of them is an error with the pick commands, not a
  fallback.

### Why invocation does not use the preference order

`PreferRoutes` exists and ranks routes sensibly, and it is used to choose what
to PROBE. It is deliberately NOT used to choose what to INVOKE.

The asymmetry is the whole reason the old behavior was a bug rather than a
heuristic: a wrong guess when probing costs one probe and reports a failure,
while a wrong guess when invoking silently sends a user's real query to an
endpoint their account cannot serve. Refusal is only annoying in the first
case; in the second it is the correct answer.

## Alternatives considered

**Nested `routes:` under each model.** Rejected: `ModelInfo` is the single wire
type for YAML, `--json`, and the Swift decoder, and nesting forces a "which
route?" decision into ~20 call sites that need none. It is also more dangerous
on disk: YAML decoding drops unknown keys, so an older binary reading a nested
row would see the model with all routing gone, whereas a flat row still carries
`region` and `invoke_strategy`, which already existed in v1.

**Inferring the missing plane for legacy rows.** Rejected for invocation. A
ladder (mantle strategy, then `us.`/`global.` prefix, then builtin id match,
else classic) resolves most rows, but its last rung is exactly the guess that
caused the incidents. Only the first three rungs are used, and only to keep a
legacy row matching its builtin so price overrides and enable flags survive; a
row that reaches the end stays an unpinned template and the invoke path refuses
it. The route it should have is DISCOVERED, not deduced.

**Keeping the base-match resolver with narrower vetoes.** Rejected as the
long-term answer: three vetoes had already accreted, each after an incident,
and with plane in the key a classic lookup cannot reach a mantle row at all, so
the mechanism has nothing left to arbitrate. It is superseded rather than tuned
— but see Consequences: it is not yet deleted.

## Consequences

- The four collapse sites are gone; `dedupeDiscoveredBedrock` becomes a pure
  route-key dedupe, its "classic wins" instinct demoted to a `PreferRoutes`
  tiebreak that orders without deleting.
- The profile-stripped base-match resolver and its three vetoes become
  **redundant, but are NOT yet deleted**. `resolveCatalogString`,
  `findCatalogString`, `effectiveInvokeStrategy`, `ResolveModelRegion`,
  `EffectiveBedrockRegion`, `carryVaultRegionPin`, and `persistProbedRegion`
  all still exist and still run: `ResolveSlotRoute` decides the route ahead of
  them, so they now only ever confirm a decision already made, but they remain
  a second way to answer the same question and should be removed in a
  follow-up. Until then, "identity is a route" holds for catalog operations
  (save, overlay, merge, active-marking, slot resolution) while those legacy
  helpers still key on `(provider, id)`.
  `inferenceProfileBaseID` survives regardless, for its legitimate job
  (discovery-time dedupe of a base model already covered by a profile).
- `ModelInfo.Region` had two owners that "must never fight over the field".
  The conflict dissolves because region is now part of the key rather than a
  shared mutable pin, but note that it IS still written in two places:
  `persistProbedRegion` stamps the plane and region a probe actually used, and
  `AdoptRoutingHints` fills an empty one from a discovered row. The first is
  AUTHORITATIVE (the probe result is by definition the truth about which
  endpoint was called, and fill-only-empty there let a stale route block its
  own correction); the second stays fill-only-empty. Both write the row's OWN
  route, so they record identity rather than compete to redefine it.
- Unpinned rows (region-agnostic builtins, and rows predating routes) are
  retired once concrete routes cover them, so a model is never listed both
  ways. Their Enabled state propagates first, so retiring a template cannot
  resurrect a model the user disabled.
- The discovery cache version is bumped: entries written before this hold rows
  with no route.
- `documents.embedding_model` is deliberately untouched. It stores a bare model
  id compared with `!=` for staleness, and the mantle plane is generation-only,
  so every embedding route is classic and the route never needs to enter that
  string. Changing its shape would make every vault re-embed from scratch, at
  real cost.
